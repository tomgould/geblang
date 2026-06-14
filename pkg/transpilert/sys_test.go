package transpilert

import (
	"runtime"
	"testing"
)

func TestSysPlatformArch(t *testing.T) {
	if SysPlatform() != runtime.GOOS {
		t.Error("platform mismatch")
	}
	if SysArch() != runtime.GOARCH {
		t.Error("arch mismatch")
	}
}

func TestSysGetenvNullOnMiss(t *testing.T) {
	SysSetenv("GB_TRANSPILERT_TEST", "v")
	if SysGetenv("GB_TRANSPILERT_TEST") != "v" {
		t.Error("getenv set value wrong")
	}
	if SysGetenv("GB_TRANSPILERT_MISSING_XYZ") != nil {
		t.Error("getenv miss should be nil (null)")
	}
}

func TestSysEnvironContainsSetVar(t *testing.T) {
	SysSetenv("GB_TRANSPILERT_ENVTEST", "x")
	env := SysEnviron()
	if v, ok := env.Get("GB_TRANSPILERT_ENVTEST"); !ok || v != "x" {
		t.Errorf("environ missing set var: %v %v", v, ok)
	}
}
