package loop

import "testing"

func TestDecodeRecord(t *testing.T) {
	line := []byte(`{"iter":7,"state":"clean","narrative":"hello","unknown_field":42}`)
	rec, raw, err := DecodeRecord(line)
	if err != nil {
		t.Fatalf("DecodeRecord: %v", err)
	}
	if rec.Iter != 7 || rec.State != "clean" || rec.Narrative != "hello" {
		t.Errorf("rec = %+v, want iter=7 state=clean narrative=hello", rec)
	}
	// The raw map preserves fields not modeled on IterRecord, so
	// pretty-printing survives forward-compat.
	if raw["unknown_field"] != float64(42) {
		t.Errorf("raw[unknown_field] = %v, want 42", raw["unknown_field"])
	}
}

func TestDecodeRecordMalformed(t *testing.T) {
	if _, _, err := DecodeRecord([]byte(`{not json`)); err == nil {
		t.Error("malformed line: nil err, want failure")
	}
}
