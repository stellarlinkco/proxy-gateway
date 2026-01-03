package handlers

import (
	"testing"
	"time"
)

func TestParseDurationParam(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{in: "1h", want: time.Hour},
		{in: "7d", want: 7 * 24 * time.Hour},
		{in: "0d", wantErr: true},
		{in: "-1d", wantErr: true},
		{in: "bad", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseDurationParam(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got != tt.want {
				t.Fatalf("got=%s want=%s", got, tt.want)
			}
		})
	}
}
