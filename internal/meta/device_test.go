package meta

import "testing"

func TestDeviceKey(t *testing.T) {
	cases := []struct {
		name string
		m    Metadata
		want string
	}{
		{
			name: "make+model+serial",
			m:    Metadata{Make: "FUJIFILM", Model: "X-T5", SerialNumber: "A1B2C3D4"},
			want: "FUJIFILM X-T5#A1B2C3D4",
		},
		{
			name: "make+model only",
			m:    Metadata{Make: "DJI", Model: "FC7303"},
			want: "DJI FC7303#",
		},
		{
			name: "missing make",
			m:    Metadata{Model: "X-T5", SerialNumber: "A1B2C3D4"},
			want: "Unknown",
		},
		{
			name: "missing model",
			m:    Metadata{Make: "FUJIFILM", SerialNumber: "A1B2C3D4"},
			want: "Unknown",
		},
		{
			name: "all empty",
			m:    Metadata{},
			want: "Unknown",
		},
		{
			name: "whitespace-only make",
			m:    Metadata{Make: "  ", Model: "X-T5"},
			want: "Unknown",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DeviceKey(tc.m)
			if got != tc.want {
				t.Errorf("DeviceKey: got %q want %q", got, tc.want)
			}
		})
	}
}
