package adapter

import "gopkg.in/yaml.v3"

type Yaml struct{}

func (a Yaml) Encode(v any) ([]byte, error) {
    return yaml.Marshal(v)
}

func (a Yaml) Decode(b []byte, v any) error {
    return yaml.Unmarshal(b, v)
}
