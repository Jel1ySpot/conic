package adapter

type Adapter interface {
    Encode(v any) ([]byte, error)
    Decode(b []byte, v any) error
}
