package process

import (
	"runtime"
	"testing"
)

func TestMinimalProbeEnvUsesPlatformAllowlist(t *testing.T) {
	env := []string{
		"PATH=path-value",
		"path=lowercase-path-value",
		"SystemRoot=system-root-value",
		"systemroot=lowercase-system-root-value",
		"WINDIR=windir-value",
		"windir=lowercase-windir-value",
		"TEMP=temp-value",
		"TMP=tmp-value",
		"PATHEXT=.COM;.EXE",
		"ComSpec=comspec-value",
		"TMPDIR=tmpdir-value",
		"LANG=en_US.UTF-8",
		"LC_ALL=C",
		"TZ=UTC",
		"OPENAI_API_KEY=openai-secret",
		"openai_api_key=lowercase-openai-secret",
		"ANTHROPIC_API_KEY=anthropic-secret",
		"GEMINI_API_KEY=gemini-secret",
		"XAI_API_KEY=xai-secret",
		"AWS_SECRET_ACCESS_KEY=aws-secret",
		"GH_TOKEN=github-secret",
		"CUSTOM_SECRET=custom-secret",
		"HOME=home-value",
		"home=lowercase-home-value",
		"USERPROFILE=profile-value",
		"APPDATA=appdata-value",
		"XDG_CONFIG_HOME=config-home-value",
		"ROBOREV_PROVIDER_TOKEN=provider-secret",
		"CLOUD_CREDENTIALS=cloud-secret",
		"FORGE_TOKEN=forge-secret",
		"NODE_OPTIONS=--require=untrusted.js",
		"LD_PRELOAD=untrusted.so",
		"LD_AUDIT=untrusted-audit.so",
		"DYLD_INSERT_LIBRARIES=untrusted.dylib",
		"=C:=drive-current-directory",
	}

	got := MinimalProbeEnv(env)
	gotSet := make(map[string]struct{}, len(got))
	for _, entry := range got {
		gotSet[entry] = struct{}{}
	}

	want := map[string]struct{}{
		"PATH=path-value": {},
	}
	if runtime.GOOS == "windows" {
		for _, entry := range []string{
			"path=lowercase-path-value",
			"SystemRoot=system-root-value",
			"systemroot=lowercase-system-root-value",
			"WINDIR=windir-value",
			"windir=lowercase-windir-value",
			"TEMP=temp-value",
			"TMP=tmp-value",
			"PATHEXT=.COM;.EXE",
			"ComSpec=comspec-value",
		} {
			want[entry] = struct{}{}
		}
	} else {
		for _, entry := range []string{
			"TMPDIR=tmpdir-value",
			"TMP=tmp-value",
			"TEMP=temp-value",
			"LANG=en_US.UTF-8",
			"LC_ALL=C",
			"TZ=UTC",
		} {
			want[entry] = struct{}{}
		}
	}

	if len(gotSet) != len(want) {
		t.Fatalf("MinimalProbeEnv() = %#v, want exactly %#v", got, want)
	}
	for entry := range want {
		if _, ok := gotSet[entry]; !ok {
			t.Errorf("MinimalProbeEnv() omitted allowed entry %q; got %#v", entry, got)
		}
	}

	for _, entry := range []string{
		"OPENAI_API_KEY=openai-secret",
		"openai_api_key=lowercase-openai-secret",
		"ANTHROPIC_API_KEY=anthropic-secret",
		"GEMINI_API_KEY=gemini-secret",
		"XAI_API_KEY=xai-secret",
		"AWS_SECRET_ACCESS_KEY=aws-secret",
		"GH_TOKEN=github-secret",
		"CUSTOM_SECRET=custom-secret",
		"HOME=home-value",
		"home=lowercase-home-value",
		"USERPROFILE=profile-value",
		"APPDATA=appdata-value",
		"XDG_CONFIG_HOME=config-home-value",
		"ROBOREV_PROVIDER_TOKEN=provider-secret",
		"CLOUD_CREDENTIALS=cloud-secret",
		"FORGE_TOKEN=forge-secret",
		"NODE_OPTIONS=--require=untrusted.js",
		"LD_PRELOAD=untrusted.so",
		"LD_AUDIT=untrusted-audit.so",
		"DYLD_INSERT_LIBRARIES=untrusted.dylib",
		"=C:=drive-current-directory",
	} {
		if _, ok := gotSet[entry]; ok {
			t.Errorf("MinimalProbeEnv() leaked forbidden entry %q", entry)
		}
	}

	if runtime.GOOS != "windows" {
		for _, entry := range []string{
			"path=lowercase-path-value",
			"systemroot=lowercase-system-root-value",
			"windir=lowercase-windir-value",
		} {
			if _, ok := gotSet[entry]; ok {
				t.Errorf("MinimalProbeEnv() accepted case-variant %q on %s", entry, runtime.GOOS)
			}
		}
	}
}

func TestMinimalProbeEnvAlwaysReturnsNonNil(t *testing.T) {
	for _, env := range [][]string{
		nil,
		{},
		{"OPENAI_API_KEY=secret"},
	} {
		got := MinimalProbeEnv(env)
		if got == nil {
			t.Fatalf("MinimalProbeEnv(%#v) returned nil", env)
		}
		if len(got) != 0 {
			t.Fatalf("MinimalProbeEnv(%#v) = %#v, want empty", env, got)
		}
	}
}
