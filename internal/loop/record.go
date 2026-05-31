package loop

import "encoding/json"

// DecodeRecord parses one summary.jsonl line into the canonical
// IterRecord and (in parallel) a raw map so unknown fields survive
// re-serialization (e.g. pretty-printing). Both unmarshals must
// succeed; a malformed line returns the error so the caller can skip
// it. summary.jsonl is the schema's home, so the read-side commands
// (logs/timeline/report) share this one decoder.
func DecodeRecord(line []byte) (IterRecord, map[string]any, error) {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return IterRecord{}, nil, err
	}
	var rec IterRecord
	if err := json.Unmarshal(line, &rec); err != nil {
		return IterRecord{}, nil, err
	}
	return rec, raw, nil
}
