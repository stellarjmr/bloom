package bloom

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOfficialUninstallerVendorCoversSecurityAndRemoteAccessApps(t *testing.T) {
	cases := []struct {
		name, bundleID, want string
	}{
		{"TeamViewer", "com.teamviewer.TeamViewer", "TeamViewer"},
		{"Adobe Genuine Service", "com.adobe.gcclient.app", "Adobe Genuine Service"},
		{"Avast Security", "com.avast.avast", "Avast"},
		{"Avast One", "com.avast.hub", "Avast"},
		{"AVG AntiVirus", "com.example.avg", "AVG"},
		{"Malwarebytes", "com.malwarebytes.mbam.frontend.application", "Malwarebytes"},
		{"Bitdefender Antivirus for Mac", "com.bitdefender.antivirusformac", "Bitdefender"},
		{"VirusBarrier", "com.intego.virusbarrier.app", "Intego"},
		{"VirusBarrier X9", "com.intego.virusbarrier-x9", "Intego"},
		{"NetBarrier", "com.intego.app.netbarrier", "Intego"},
		{"Kaspersky Internet Security", "com.kaspersky.kav", "Kaspersky"},
		{"Norton 360", "com.symantec.nfm", "Norton"},
		{"Trend Micro Antivirus", "com.trendmicro.tmsm.app", "Trend Micro"},
		{"AliEntSafe", "com.alibaba.security.AliEntSafe", "Alibaba Cloud Security"},
		// The same vendors' ordinary apps stay uninstallable.
		{"Kaspersky Password Manager", "com.kaspersky.kpm", ""},
		{"Washing Machine", "com.intego.WashingMachine", ""},
		{"Bitdefender Virus Scanner", "com.bitdefender.virusscanner", ""},
		{"DingTalk", "com.alibaba.DingTalkMac", ""},
		{"AliyunDrive", "com.alibaba.aliyun.drive", ""},
		{"Adobe Photoshop", "com.adobe.Photoshop", ""},
		{"Avast Passwords", "com.avast.passwords", ""},
	}
	for _, tt := range cases {
		app := AppEntry{Name: tt.name, BundleID: tt.bundleID, Path: "/Applications/" + tt.name + ".app"}
		if got := officialUninstallerVendor(app); got != tt.want {
			t.Errorf("officialUninstallerVendor(%q, %q) = %q, want %q", tt.name, tt.bundleID, got, tt.want)
		}
	}
}

func TestProcessIDsMatchTheExecutableNameLiterally(t *testing.T) {
	cases := map[string]string{
		"Foo":           "/usr/bin/pgrep -x -- Foo",
		"zoom.us":       `/usr/bin/pgrep -x -- zoom\.us`,
		"Foo (Beta)+":   `/usr/bin/pgrep -x -- Foo \(Beta\)\+`,
		".*":            `/usr/bin/pgrep -x -- \.\*`,
		`a[b]{c}^$|?\d`: `/usr/bin/pgrep -x -- a\[b\]\{c\}\^\$\|\?\\d`,
		// Behind the --, pgrep's own flags are ordinary names.
		"-l":   "/usr/bin/pgrep -x -- -l",
		"-foo": "/usr/bin/pgrep -x -- -foo",
		// Control bytes and invalid UTF-8 match themselves and stay intact.
		"Foo\tBar":   "/usr/bin/pgrep -x -- Foo\tBar",
		"Foo\x7fBar": "/usr/bin/pgrep -x -- Foo\x7fBar",
		"Foo\xffBar": "/usr/bin/pgrep -x -- Foo\xffBar",
	}
	for name, want := range cases {
		r := &recordingRunner{outputs: map[string]CommandOutput{}}
		processIDs(context.Background(), r, name)
		if len(r.calls) != 1 || r.calls[0] != want {
			t.Errorf("processIDs(%q) ran %#v, want %q", name, r.calls, want)
		}
	}
	r := &recordingRunner{outputs: map[string]CommandOutput{}}
	if pids := processIDs(context.Background(), r, ""); len(pids) != 0 || len(r.calls) != 0 {
		t.Errorf("processIDs(\"\") = %#v after %#v, want no pgrep without a name", pids, r.calls)
	}
}

func TestStopAppQuitsARunningAppWhateverItsExecutableIsNamed(t *testing.T) {
	oldPoll := stopAppPollInterval
	stopAppPollInterval = 0
	t.Cleanup(func() { stopAppPollInterval = oldPoll })
	for _, executable := range []string{"-l", "Foo\tBar", "Foo (Beta"} {
		home := t.TempDir()
		t.Setenv("HOME", home)
		appPath := filepath.Join(home, "Applications", "Foo.app")
		writeTestInfoPlist(t, appPath, "com.example.foo", executable)
		r := &processRunner{
			pids:         map[string][]string{processPatternLiteral(executable): {"123"}},
			processPaths: map[string]string{"123": filepath.Join(appPath, "Contents", "MacOS", executable)},
		}

		stopApp(context.Background(), r, AppEntry{Path: appPath, Name: "Foo", BundleID: "com.example.foo"}, nil)
		if !runnerCallContains(r.calls, `tell application id "com.example.foo" to quit`) {
			t.Errorf("executable %q: a running app got no graceful quit: %#v", executable, r.calls)
		}
	}
}
