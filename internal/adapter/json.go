package adapter

import "encoding/json"

type Json struct{}

func (a Json) Encode(v any) ([]byte, error) {
    return json.MarshalIndent(v, "", "  ")
}

func (a Json) Decode(b []byte, v any) error {
    return json.Unmarshal(b, v)
}
