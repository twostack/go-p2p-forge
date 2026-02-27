package forge

import "encoding/json"

// JSONCodec implements Codec using encoding/json.
type JSONCodec struct{}

// Marshal encodes v as JSON.
func (JSONCodec) Marshal(v any) ([]byte, error) { return json.Marshal(v) }

// Unmarshal decodes JSON data into v.
func (JSONCodec) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// ContentType returns "json".
func (JSONCodec) ContentType() string { return "json" }
