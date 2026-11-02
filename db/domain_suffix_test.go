package db

import (
	"reflect"
	"testing"
)

func TestNormalizeDomainSuffixes(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "canonicalizes wildcard, leading and trailing dots and case",
			in:   []string{" Aedev.Feishu.CN ", "*.aedev.feishu.cn", ".aedev.feishu.cn", "", "aedev.feishu.cn."},
			want: []string{"aedev.feishu.cn"},
		},
		{
			name: "keeps distinct suffixes in order",
			in:   []string{"aedev.feishu.cn", "aedev.feishuapp.cn"},
			want: []string{"aedev.feishu.cn", "aedev.feishuapp.cn"},
		},
		{
			name: "empty and nil become empty",
			in:   nil,
			want: []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeDomainSuffixes(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("normalizeDomainSuffixes(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
