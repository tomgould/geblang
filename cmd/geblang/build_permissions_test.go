package main

import (
	"reflect"
	"testing"

	"geblang/internal/bundle"
	"geblang/internal/ffi"
	"geblang/internal/modules"
)

func TestResolveBundlePermissions(t *testing.T) {
	ffiCfg := &ffi.PolicyConfig{Enabled: true, Libraries: []ffi.PolicyLibraryConfig{{Path: "/usr/lib/a.so"}, {Glob: "/opt/*.so"}}}
	cases := []struct {
		name       string
		m          modules.ManifestPermissions
		cliFFI     []string
		cliOnnx    bool
		cliProc    bool
		cliBrowser bool
		want       *bundle.Permissions
	}{
		{"empty is nil", modules.ManifestPermissions{}, nil, false, false, false, nil},
		{"manifest ffi + onnx", modules.ManifestPermissions{FFI: ffiCfg, Onnx: true}, nil, false, false, false,
			&bundle.Permissions{FFI: []string{"/usr/lib/a.so", "/opt/*.so"}, Onnx: true}},
		{"cli only", modules.ManifestPermissions{}, []string{"/x/*.so"}, false, true, false,
			&bundle.Permissions{FFI: []string{"/x/*.so"}, ProcessControl: true}},
		{"manifest and cli merge", modules.ManifestPermissions{FFI: ffiCfg, ProcessControl: true}, []string{"/x/*.so"}, true, false, false,
			&bundle.Permissions{FFI: []string{"/usr/lib/a.so", "/opt/*.so", "/x/*.so"}, Onnx: true, ProcessControl: true}},
		{"browser via manifest + cli", modules.ManifestPermissions{Browser: true}, nil, false, false, true,
			&bundle.Permissions{Browser: true}},
		{"disabled ffi is not baked", modules.ManifestPermissions{FFI: &ffi.PolicyConfig{Enabled: false, Libraries: []ffi.PolicyLibraryConfig{{Path: "/y.so"}}}}, nil, false, false, false, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveBundlePermissions(c.m, c.cliFFI, c.cliOnnx, c.cliProc, c.cliBrowser)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}
}
