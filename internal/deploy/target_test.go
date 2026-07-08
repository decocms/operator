package deploy

import "testing"

func TestIsContentOnly(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  bool
	}{
		{"empty list is not content", nil, false},
		{"blocks only", []string{".deco/blocks/pages-home.json"}, true},
		{"gen snapshot only", []string{"src/server/cms/blocks.gen.json"}, true},
		{"blocks + gen snapshot (studio commit)", []string{
			".deco/blocks/pages-home.json",
			".deco/blocks/loaders-products.json",
			"src/server/cms/blocks.gen.json",
		}, true},
		{"mixed with code", []string{".deco/blocks/pages-home.json", "src/components/Header.tsx"}, false},
		{"code only", []string{"src/components/Header.tsx"}, false},
		{"sibling dir does not match prefix", []string{".deco/blocks-old/x.json"}, false},
		{"other gen files are code", []string{"src/server/cms/sections.gen.ts"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isContentOnly(tc.files); got != tc.want {
				t.Fatalf("isContentOnly(%v) = %v, want %v", tc.files, got, tc.want)
			}
		})
	}
}

func TestDecofileName(t *testing.T) {
	cases := []struct {
		site string
		want string
	}{
		{"storefront-tanstack", "fastdeploy-storefront-tanstack"},
		{"My_Site.X", "fastdeploy-my-site-x"},
		{"", "fastdeploy-site"},
	}
	for _, tc := range cases {
		if got := decofileName(tc.site); got != tc.want {
			t.Fatalf("decofileName(%q) = %q, want %q", tc.site, got, tc.want)
		}
	}
	// 63-char cap holds for absurdly long site names.
	long := decofileName("this-is-a-very-long-site-name-that-would-overflow-the-kubernetes-limit")
	if len(long) > 63 {
		t.Fatalf("decofileName exceeded 63 chars: %d", len(long))
	}
}
