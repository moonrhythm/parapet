package headers

// Header type
type Header struct {
	Key   string
	Value string
}

func buildHeaders(pairs []string) []Header {
	var hs []Header
	for i := 1; i < len(pairs); i += 2 {
		hs = append(hs, Header{
			Key:   pairs[i-1],
			Value: pairs[i],
		})
	}
	return hs
}
