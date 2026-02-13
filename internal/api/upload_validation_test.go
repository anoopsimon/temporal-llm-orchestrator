package api

import "testing"

func TestIsSupportedTextUpload(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body []byte
		want bool
	}{
		{
			name: "plain text",
			body: []byte("payslip\nemployee: Jane Doe\ngross pay: 2000\nnet pay: 1500\n"),
			want: true,
		},
		{
			name: "empty",
			body: []byte(""),
			want: false,
		},
		{
			name: "whitespace only",
			body: []byte(" \n\t "),
			want: false,
		},
		{
			name: "invalid utf8",
			body: []byte{0xff, 0xfe, 0xfd},
			want: false,
		},
		{
			name: "pdf header",
			body: []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\n%%EOF\n"),
			want: false,
		},
		{
			name: "png header",
			body: []byte{
				0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
				0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
				0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
			},
			want: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := isSupportedTextUpload(tc.body)
			if got != tc.want {
				t.Fatalf("isSupportedTextUpload() = %v, want %v", got, tc.want)
			}
		})
	}
}
