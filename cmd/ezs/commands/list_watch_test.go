package commands

import (
	"reflect"
	"testing"
)

func TestParseWatchFlag(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantN    int
		wantRest []string
	}{
		{
			name:     "no watch",
			args:     []string{"-a", "--json"},
			wantN:    0,
			wantRest: []string{"-a", "--json"},
		},
		{
			name:     "bare --watch default 5",
			args:     []string{"--watch"},
			wantN:    5,
			wantRest: nil,
		},
		{
			name:     "--watch 10 eats next arg",
			args:     []string{"--watch", "10"},
			wantN:    10,
			wantRest: nil,
		},
		{
			name:     "--watch=10",
			args:     []string{"--watch=10"},
			wantN:    10,
			wantRest: nil,
		},
		{
			name:     "--watch=abc falls back to 5",
			args:     []string{"--watch=abc"},
			wantN:    5,
			wantRest: nil,
		},
		{
			name:     "preserves other args in order",
			args:     []string{"-a", "--watch", "-b", "foo"},
			wantN:    5,
			wantRest: []string{"-a", "-b", "foo"},
		},
		{
			name:     "--watch 7 with other args",
			args:     []string{"--watch", "7", "-a"},
			wantN:    7,
			wantRest: []string{"-a"},
		},
		{
			name:     "--watch followed by non-int leaves next arg",
			args:     []string{"--watch", "foo"},
			wantN:    5,
			wantRest: []string{"foo"},
		},
		{
			name:     "--watch=0 falls back to 5",
			args:     []string{"--watch=0"},
			wantN:    5,
			wantRest: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotN, gotRest := parseWatchFlag(tc.args)
			if gotN != tc.wantN {
				t.Errorf("interval = %d, want %d", gotN, tc.wantN)
			}
			if !reflect.DeepEqual(gotRest, tc.wantRest) {
				t.Errorf("rest = %#v, want %#v", gotRest, tc.wantRest)
			}
		})
	}
}
