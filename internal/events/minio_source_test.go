package events

import "testing"

func TestParseObjectKey(t *testing.T) {
	tests := []struct {
		name      string
		objectKey string
		wantDocID string
		wantFile  string
		wantErr   bool
	}{
		{name: "valid", objectKey: "abc-123/payslip.txt", wantDocID: "abc-123", wantFile: "payslip.txt"},
		{name: "valid nested", objectKey: "abc-123/nested/path/file.txt", wantDocID: "abc-123", wantFile: "nested/path/file.txt"},
		{name: "invalid no slash", objectKey: "abc-123", wantErr: true},
		{name: "invalid empty", objectKey: "", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			docID, filename, err := parseObjectKey(tc.objectKey)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if docID != tc.wantDocID {
				t.Fatalf("docID mismatch: got %q want %q", docID, tc.wantDocID)
			}
			if filename != tc.wantFile {
				t.Fatalf("filename mismatch: got %q want %q", filename, tc.wantFile)
			}
		})
	}
}

func TestDecodeObjectKey(t *testing.T) {
	decoded, err := decodeObjectKey("abc-123%2Fpayslip%20final.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decoded != "abc-123/payslip final.txt" {
		t.Fatalf("decoded mismatch: got %q", decoded)
	}
}
