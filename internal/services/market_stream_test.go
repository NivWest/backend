package services

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestParseStreamIDs(t *testing.T) {
	many := func(n int) string {
		ids := make([]string, n)
		for i := range ids {
			ids[i] = fmt.Sprintf("SIM-%02d", i)
		}
		return strings.Join(ids, ",")
	}
	tests := []struct {
		name    string
		raw     string
		want    []string
		wantErr bool
	}{
		{name: "two ids", raw: "SIM-FJRD,SIM-NVE", want: []string{"SIM-FJRD", "SIM-NVE"}},
		{name: "trimmed, blanks ignored", raw: " SIM-FJRD , ,SIM-NVE,", want: []string{"SIM-FJRD", "SIM-NVE"}},
		{name: "duplicates once, first order", raw: "B,A,B", want: []string{"B", "A"}},
		{name: "exactly 50", raw: many(50), want: strings.Split(many(50), ",")},
		{name: "empty", raw: "", wantErr: true},
		{name: "only blanks", raw: " , ,", wantErr: true},
		{name: "51 entries", raw: many(51), wantErr: true},
		{name: "51 entries counting duplicates", raw: many(50) + ",SIM-00", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseStreamIDs(tt.raw)
			if tt.wantErr {
				expectValidationError(t, err)
				return
			}
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseStreamIDs(%q) = %v, %v; want %v", tt.raw, got, err, tt.want)
			}
		})
	}
}
