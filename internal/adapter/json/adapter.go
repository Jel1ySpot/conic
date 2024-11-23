package json

import "encoding/json"

type Adapter struct{}

func (a Adapter) Encode(v any) ([]byte, error) {
    return json.MarshalIndent(v, "", "  ")
}

func (a Adapter) Decode(b []byte, v any) error {
    return json.Unmarshal(b, v)
}
