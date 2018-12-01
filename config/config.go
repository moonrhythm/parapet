package config

import (
	"bytes"
	"io/ioutil"

	"gopkg.in/yaml.v2"
)

// Config is the config loader
type Config struct {
	raw []byte
}

// New creates new config from bytes
func New(b []byte) Config {
	return Config{raw: b}
}

// NewFromFile creates new config from file
func NewFromFile(filename string) (Config, error) {
	b, err := ioutil.ReadFile(filename)
	return New(b), err
}

// Scope scopes config
func (c Config) Scope(name string) Config {
	if name == "" {
		return c
	}

	var p map[string]interface{}
	c.Load(&p)
	b, _ := yaml.Marshal(p[name])
	return New(b)
}

// Load loads a config for given name
func (c Config) Load(v interface{}) error {
	return yaml.
		NewDecoder(bytes.NewReader(c.raw)).
		Decode(v)
}
