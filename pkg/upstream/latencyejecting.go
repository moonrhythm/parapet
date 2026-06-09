package upstream

import (
	"math"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Latency-ejecting load balancer defaults.
const (
	defaultEjectionFactor     = 3.0
	defaultLatMinSamples      = 100
	defaultLatMinHosts        = 3
	defaultHalfLife           = 10 * time.Second
	defaultMinEjectDelta      = 50 * time.Millisecond
	defaultMaxEjectionPercent = 30
	defaultPanicThreshold     = 50
	// EjectTimeout / MaxEjectTimeout reuse ejecting.go's defaultEjectTimeout (30s)
	// and defaultMaxEjectTimeout (5m).
)

// NewLatencyEjectingLoadBalancer creates a round-robin load balancer with passive
// latency-based outlier ejection.
func NewLatencyEjectingLoadBalancer(targets []*Target) *LatencyEjectingLoadBalancer {
	return &LatencyEjectingLoadBalancer{Targets: targets}
}

// LatencyEjectingLoadBalancer is a round-robin load balancer that ejects a "gray
// failure" — a target that keeps returning 200s but whose mean time-to-first-byte
// has crept far above its peers, which error-based ejection and the circuit breaker
// both miss. A target whose decayed mean TTFB exceeds EjectionFactor x the pool
// median (and a small absolute slack) is taken out of rotation for a backed-off
// cooldown and passively re-probed, reusing EjectingLoadBalancer's eject mechanics.
//
// It is LATENCY-ONLY: a transport error is not timed (a ~0ns connection-refused
// would falsely lower the mean and make a dead host look fast) and does not eject a
// target here. An Upstream uses one balancer, so if hard errors are your dominant
// failure mode use EjectingLoadBalancer or the circuit breaker INSTEAD (they do not
// catch gray failures) — choose by failure mode. When all targets are ejected, or
// when more than
// PanicThreshold% are (a systemic slowdown), it FAILS OPEN and routes to all,
// including the slow ones: a slow-but-up host beats a 503. This is deliberately the
// opposite of CircuitBreakingLoadBalancer, which sheds load — appropriate because a
// latency outlier is still serving.
//
// Detection is relative-to-pool, so it self-tunes: a uniform slowdown raises every
// target and the median together, so no one is an outlier. Two structural limits
// follow from that: a pool of fewer than MinHosts never latency-ejects (you cannot
// name an outlier without a baseline), and a slowdown affecting a MAJORITY of the
// pool is not detected (the slow hosts become the median) — that is a systemic
// event, handled by failing open, not a per-host outlier. It also tracks the MEAN,
// so a tail-only regression (healthy median, fat p99) is not caught, and it assumes
// targets see statistically similar traffic — a target fronting heavier routes can
// read as a false outlier. Weight is ignored. State is per-target atomics; the hot
// path is lock-free. Configuration fields are read once; set them before serving.
//
//nolint:govet // fields grouped by role (state, then config) for readability
type LatencyEjectingLoadBalancer struct {
	once  sync.Once
	i     atomic.Uint32 // round-robin cursor
	tau   float64       // HalfLife / ln2, precomputed in init
	peers []latPeer     // per-target state, built in init

	// Targets is the set of upstreams to balance across.
	Targets []*Target

	// EjectionFactor ejects a target whose decayed mean TTFB is >= the pool median
	// times this. Must be > 1 (a factor <= 1 would eject the median itself).
	// Defaults to 3.0; below ~2.0 is aggressive (ejects on normal tail jitter).
	EjectionFactor float64

	// MinSamples is the number of measured round-trips a target must accumulate
	// before it is eligible to be ejected or to count toward the pool median. It
	// gates cold-start noise (a first request's TLS handshake / cold pool) and, since
	// a re-probed target's sample count is reset on ejection, also gates re-probes.
	// Defaults to 100.
	MinSamples uint64

	// MinHosts is the minimum number of eligible targets for a valid pool median; a
	// pool with fewer never latency-ejects. Defaults to 3 (a median needs >= ~3 to be
	// robust to the outlier it is meant to expose).
	MinHosts int

	// HalfLife is the wall-clock half-life of the time-decayed latency EWMA, so the
	// effective window is independent of a target's request rate. Defaults to 10s.
	HalfLife time.Duration

	// MinEjectDelta is an additive floor: a target must also exceed the median by at
	// least this to be ejected, which kills the ratio instability of a fast pool (3ms
	// is 3x a 1ms median but operationally irrelevant). Defaults to 50ms.
	MinEjectDelta time.Duration

	// MinEjectLatency is an absolute floor: a target below it is never ejected,
	// however slow it is relative to peers. Defaults to 0 (off).
	MinEjectLatency time.Duration

	// MaxEjectionPercent caps the fraction of the pool that may be latency-ejected at
	// once, so the pool cannot progressively drain. Defaults to 30. It is a near-hard
	// cap: a concurrent burst of decisions can transiently overshoot it by the
	// in-flight concurrency (the counts are lock-free scans), self-correcting as
	// cooldowns expire — the exact anti-oscillation guarantee comes from the median
	// being robust, not from this count.
	MaxEjectionPercent int

	// PanicThreshold: if more than this percent of the pool is ejected, the slowdown
	// is treated as systemic — no further ejections happen and pick routes to all
	// targets (including slow ones). Defaults to 50; init raises it above
	// MaxEjectionPercent so it is never dead code.
	PanicThreshold int

	// EjectTimeout is the base cooldown a target stays ejected, doubling on each
	// repeat ejection up to MaxEjectTimeout. Defaults to 30s.
	EjectTimeout time.Duration

	// MaxEjectTimeout caps the ejection cooldown. Defaults to 5m.
	MaxEjectTimeout time.Duration

	// OnStateChange observes a target being ejected (ReasonEject) or healed back into
	// rotation (ReasonRecover); nil disables it. Like EjectingLoadBalancer, it
	// reflects committed eject/recover events, not cooldown-expiry rotation
	// membership. See prom.UpstreamState. The callee owns its own concurrency.
	OnStateChange StateChangeFunc
}

// latPeer holds one target's latency-ejection state. ewmaBits is the single source
// of truth for the decayed mean TTFB.
//
//nolint:govet // fields grouped by role for readability
type latPeer struct {
	target       *Target
	ewmaBits     atomic.Uint64 // math.Float64bits(decayed mean TTFB, ns); 0 = unseeded
	lastNanos    atomic.Int64  // unix nanos basis of the committed ewma (decay dt)
	samples      atomic.Uint64 // measured round-trips since (re-)admission; gates eligibility
	ejections    atomic.Int32  // consecutive ejection episodes -> backoff exponent
	ejectedUntil atomic.Int64  // unix nanos; <= now means selectable
}

func (l *LatencyEjectingLoadBalancer) init() {
	if l.EjectionFactor <= 1 {
		l.EjectionFactor = defaultEjectionFactor
	}
	if l.MinSamples == 0 {
		l.MinSamples = defaultLatMinSamples
	}
	if l.MinHosts < 2 {
		l.MinHosts = defaultLatMinHosts
	}
	if l.HalfLife <= 0 {
		l.HalfLife = defaultHalfLife
	}
	if l.MinEjectDelta <= 0 {
		l.MinEjectDelta = defaultMinEjectDelta
	}
	if l.MaxEjectionPercent <= 0 || l.MaxEjectionPercent > 100 {
		l.MaxEjectionPercent = defaultMaxEjectionPercent
	}
	if l.PanicThreshold <= 0 || l.PanicThreshold > 100 {
		l.PanicThreshold = defaultPanicThreshold
	}
	if l.PanicThreshold <= l.MaxEjectionPercent {
		l.PanicThreshold = l.MaxEjectionPercent + 1 // panic must sit above the cap, never dead code
	}
	if l.EjectTimeout <= 0 {
		l.EjectTimeout = defaultEjectTimeout
	}
	if l.MaxEjectTimeout <= 0 {
		l.MaxEjectTimeout = defaultMaxEjectTimeout
	}
	if l.MaxEjectTimeout < l.EjectTimeout {
		l.MaxEjectTimeout = l.EjectTimeout
	}
	l.tau = float64(l.HalfLife) / math.Ln2

	l.peers = make([]latPeer, len(l.Targets))
	for i, t := range l.Targets {
		l.peers[i].target = t
	}
}

// RoundTrip picks a target, times the round-trip, and records the latency.
func (l *LatencyEjectingLoadBalancer) RoundTrip(r *http.Request) (*http.Response, error) {
	l.once.Do(l.init)

	n := len(l.peers)
	if n == 0 {
		return nil, ErrUnavailable
	}

	p := l.pick(n)
	r.URL.Host = p.target.Host
	start := time.Now()
	resp, err := p.target.Transport.RoundTrip(r)
	l.record(p, time.Since(start), resp, err)
	return resp, err
}

// pick selects the next selectable target round-robin, skipping ejected ones. If
// more than PanicThreshold% are ejected the slowdown is systemic — it routes to all
// targets. If somehow every target is ejected it fails open to the round-robin slot
// (never ErrUnavailable for a non-empty pool: an ejected target here is slow, not
// dead, so it is a usable fallback).
func (l *LatencyEjectingLoadBalancer) pick(n int) *latPeer {
	start := l.i.Add(1) - 1
	now := time.Now().UnixNano()
	if l.ejectedCount(now)*100 > l.PanicThreshold*n {
		return &l.peers[start%uint32(n)] // panic: distrust the signal, spread to all
	}
	for k := uint32(0); k < uint32(n); k++ {
		p := &l.peers[(start+k)%uint32(n)]
		if p.ejectedUntil.Load() <= now {
			return p
		}
	}
	return &l.peers[start%uint32(n)] // all ejected -> fail open
}

// record feeds a completed round-trip's latency into the target's EWMA and runs the
// relative-to-pool outlier test. A transport error is not a latency sample.
func (l *LatencyEjectingLoadBalancer) record(p *latPeer, d time.Duration, resp *http.Response, err error) {
	if err != nil || resp == nil {
		return // only a real response has a meaningful TTFB; errors are not this balancer's job
	}

	now := time.Now().UnixNano()
	cur, n := p.observe(float64(d), now, l.tau)
	if n < l.MinSamples {
		return // cold-start / re-probe gate
	}

	median, eligible := l.poolMedian()
	if eligible < l.MinHosts {
		return // no valid baseline: cannot name an outlier; do not heal (preserve backoff)
	}

	// Within tolerance under a valid baseline => genuine recovery (or never slow).
	if cur < median*l.EjectionFactor || cur < median+float64(l.MinEjectDelta) || cur < float64(l.MinEjectLatency) {
		l.maybeHeal(p)
		return
	}

	// Outlier. Pool-level guard rails before ejecting.
	now = time.Now().UnixNano()
	ej := l.ejectedCount(now)
	if (ej+1)*100 > l.PanicThreshold*len(l.peers) {
		return // systemic (panic): eject none
	}
	if (ej+1)*100 > l.MaxEjectionPercent*len(l.peers) {
		return // at the cap: keep it in rotation (slow but serving)
	}
	l.eject(p)
}

// observe applies one sample to the lock-free time-decayed EWMA and returns the new
// mean and the post-increment sample count.
func (p *latPeer) observe(sample float64, now int64, tau float64) (cur float64, n uint64) {
	n = p.samples.Add(1)
	for {
		oldBits := p.ewmaBits.Load()
		var next float64
		if oldBits == 0 {
			// Unseeded: seed exactly. math.Float64bits(0)==0 and no real TTFB is 0ns,
			// so 0 unambiguously means "no value yet" — do not store a computed 0.
			next = sample
		} else {
			dt := float64(now - p.lastNanos.Load())
			if dt < 0 {
				dt = 0 // clock stepped backward (NTP); skip decay for this sample
			}
			w := math.Exp(-dt / tau)
			next = w*math.Float64frombits(oldBits) + (1-w)*sample
		}
		if p.ewmaBits.CompareAndSwap(oldBits, math.Float64bits(next)) {
			p.lastNanos.Store(now) // publish the dt basis only for the committed value
			return next, n
		}
		// Lost the race; re-read the winner's value and re-decay from it.
	}
}

// poolMedian is the robust pool baseline: the median of the eligible targets'
// EWMAs. The median (not mean) is unmoved by the minority of slow hosts it exists to
// expose. It snapshots into a stack buffer (zero-alloc for small pools) off the
// latency-critical pick path.
func (l *LatencyEjectingLoadBalancer) poolMedian() (median float64, eligible int) {
	var buf [16]float64
	vals := buf[:0]
	if len(l.peers) > cap(buf) {
		vals = make([]float64, 0, len(l.peers))
	}
	for i := range l.peers {
		if l.peers[i].samples.Load() < l.MinSamples {
			continue
		}
		if b := l.peers[i].ewmaBits.Load(); b != 0 {
			vals = append(vals, math.Float64frombits(b))
		}
	}
	if len(vals) == 0 {
		return 0, 0
	}
	sort.Float64s(vals)
	m := len(vals)
	if m%2 == 1 {
		return vals[m/2], m
	}
	return (vals[m/2-1] + vals[m/2]) / 2, m
}

// ejectedCount counts targets currently ejected (a lock-free scan).
func (l *LatencyEjectingLoadBalancer) ejectedCount(now int64) (c int) {
	for i := range l.peers {
		if l.peers[i].ejectedUntil.Load() > now {
			c++
		}
	}
	return
}

// maybeHeal clears a target's ejection state on genuine recovery (called only when
// it is back within tolerance under a valid baseline). It also forgets the backoff
// exponent — full reset is correct here. The read guard keeps the healthy path
// load-only. It is NOT called when the baseline is invalid, so a transient pool
// dropout cannot reset the backoff of a genuinely-slow host.
func (l *LatencyEjectingLoadBalancer) maybeHeal(p *latPeer) {
	if p.ejections.Load() != 0 || p.ejectedUntil.Load() != 0 {
		// Swap so exactly one concurrent healer emits ReasonRecover (the winner).
		wasEjected := p.ejectedUntil.Swap(0) != 0
		p.ejections.Store(0)
		if wasEjected && l.OnStateChange != nil {
			l.OnStateChange(StateChange{Host: p.target.Host, From: StateOpen, To: StateClosed, Reason: ReasonRecover})
		}
	}
}

// eject takes a target out of rotation for a backed-off cooldown (CAS-once per
// down-window, like ejecting.go). It also FORGETS the stale latency stats so a
// recovered host seeds its EWMA fresh from the first post-cooldown sample and must
// re-accumulate MinSamples before it can be re-ejected — without this a host that
// was, say, 20x slow would re-eject on its first good sample (the decayed stale
// value still dominates) and flap.
func (l *LatencyEjectingLoadBalancer) eject(p *latPeer) {
	now := time.Now()
	for {
		prev := p.ejectedUntil.Load()
		if prev > now.UnixNano() {
			return // already ejected for this window
		}
		e := p.ejections.Load() + 1
		until := now.Add(l.ejectionTimeout(e)).UnixNano()
		if p.ejectedUntil.CompareAndSwap(prev, until) {
			p.ejections.Store(e)
			p.ewmaBits.Store(0) // re-probe seeds fresh
			p.samples.Store(0)  // re-probe must re-accumulate MinSamples before re-eligible
			if l.OnStateChange != nil {
				from := StateClosed
				if prev != 0 {
					from = StateOpen
				}
				l.OnStateChange(StateChange{Host: p.target.Host, From: from, To: StateOpen, Reason: ReasonEject})
			}
			return
		}
	}
}

// ejectionTimeout returns EjectTimeout doubled for each prior ejection, capped at
// MaxEjectTimeout. e is the 1-based ejection count.
func (l *LatencyEjectingLoadBalancer) ejectionTimeout(e int32) time.Duration {
	d := l.EjectTimeout
	for i := int32(1); i < e && d < l.MaxEjectTimeout; i++ {
		d *= 2
	}
	if d <= 0 || d > l.MaxEjectTimeout {
		return l.MaxEjectTimeout
	}
	return d
}
