package bloom

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBundleLeafDataDirVariantsExtendDisplayNameOnly(t *testing.T) {
	tests := []struct {
		name string
		app  AppEntry
		want []string
	}{
		{
			name: "ayugram",
			app:  AppEntry{Name: "AyuGram", BundleID: "one.ayugram.AyuGramDesktop"},
			want: []string{"AyuGramDesktop", "AyuGram Desktop"},
		},
		{
			name: "display name spaces remain intact",
			app:  AppEntry{Name: "Ayu Gram", BundleID: "one.ayugram.AyuGramDesktop"},
			want: []string{"AyuGramDesktop", "Ayu Gram Desktop"},
		},
		{
			name: "acronym boundary",
			app:  AppEntry{Name: "Foo", BundleID: "com.example.FooHTTPServer"},
			want: []string{"FooHTTPServer", "Foo HTTP Server"},
		},
		{
			name: "case insensitive own-name prefix",
			app:  AppEntry{Name: "AyuGram", BundleID: "one.ayugram.ayugramDesktop"},
			want: []string{"ayugramDesktop", "AyuGram Desktop"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := bundleLeafDataDirVariants(test.app); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("variants = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestBundleLeafDataDirVariantsRejectUnsafeLeaves(t *testing.T) {
	tests := []struct {
		name string
		app  AppEntry
	}{
		{name: "wrapper names another product", app: AppEntry{Name: "My Chrome SSB", BundleID: "com.wrapper.GoogleChrome"}},
		{name: "fork names another product", app: AppEntry{Name: "64Gram", BundleID: "org.fork.TelegramDesktop"}},
		{name: "unrelated product leaf", app: AppEntry{Name: "Acme Contacts Sync", BundleID: "com.acme.AddressBook"}},
		{name: "invalid bundle id", app: AppEntry{Name: "Other", BundleID: "unknown"}},
		{name: "short leaf", app: AppEntry{Name: "Example", BundleID: "com.example.app"}},
		{name: "no camel transition", app: AppEntry{Name: "Example", BundleID: "com.example.Whatsapp"}},
		{name: "leaf equals display name", app: AppEntry{Name: "CamelCaseApp", BundleID: "com.example.CamelCaseApp"}},
		{name: "leaf equals display name ignoring case", app: AppEntry{Name: "CamelCaseApp", BundleID: "com.example.camelCaseApp"}},
		{name: "suffix begins lowercase", app: AppEntry{Name: "AyuGram", BundleID: "one.ayugram.AyuGramdesktopHelper"}},
		{name: "short display name", app: AppEntry{Name: "Go", BundleID: "com.example.GoDesktop"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := bundleLeafDataDirVariants(test.app); len(got) != 0 {
				t.Fatalf("unsafe variants = %#v, want none", got)
			}
		})
	}
}

func TestFindRelatedPathsIncludesExactBundleLeafVariants(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	appPath := filepath.Join(home, "Applications", "AyuGram.app")
	writeTestInfoPlist(t, appPath, "one.ayugram.AyuGramDesktop", "AyuGram")

	variants := []string{"AyuGramDesktop", "AyuGram Desktop"}
	want := []string{}
	for _, variant := range variants {
		for _, path := range []string{
			filepath.Join(home, "Library", "Application Support", variant),
			filepath.Join(home, "Library", "Caches", variant),
			filepath.Join(home, "Library", "Logs", variant),
			filepath.Join(home, "Library", "Preferences", variant+".plist"),
			filepath.Join(home, "Library", "Saved Application State", variant+".savedState"),
		} {
			want = append(want, path)
			if filepath.Ext(path) == ".plist" {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
					t.Fatal(err)
				}
			} else if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}

	unrelated := []string{
		filepath.Join(home, "Library", "Application Support", "Ayu Gram Desktop"),
		filepath.Join(home, "Library", "Application Support", "AyuGramDesktopBackup"),
	}
	for _, path := range unrelated {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	paths := FindRelatedPaths(AppEntry{
		Path:     appPath,
		Name:     "AyuGram",
		BundleID: "one.ayugram.AyuGramDesktop",
	})
	for _, path := range want {
		if !containsString(paths, path) {
			t.Fatalf("paths missing exact bundle-leaf variant %q: %#v", path, paths)
		}
	}
	for _, path := range unrelated {
		if containsString(paths, path) {
			t.Fatalf("bundle-leaf matching reached unrelated path %q: %#v", path, paths)
		}
	}
}
