package bloom

import "testing"

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
