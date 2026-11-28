// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package pins

import (
	"strings"
	"testing"
)

// linux315 is the image's platform: Linux, CPython 3.15.
var linux315 = MarkerEnv{PythonVersion: "3.15", SysPlatform: "linux", PlatformSystem: "Linux"}

func TestEvalMarker(t *testing.T) {
	t.Parallel()
	tests := []struct {
		expr    string
		want    bool
		wantErr string
	}{
		{expr: "python_version > '3.10'", want: true},
		{expr: "sys_platform == 'win32'", want: false},
		{expr: "sys_platform != 'win32' and sys_platform != 'darwin'", want: true},
		{expr: "python_version <= '3.11'", want: false},
		// Versions compare as versions: 3.15 is past 3.9, though not as text
		{expr: "python_version > '3.9'", want: true},
		{expr: `python_version >= "3.15"`, want: true},
		{expr: "'3.10' < python_version", want: true},
		{expr: "platform_system == 'Linux'", want: true},
		{expr: "sys_platform == 'darwin' or python_version > '3.10' and sys_platform == 'linux'", want: true},
		{expr: "(sys_platform == 'darwin' or python_version > '3.10') and sys_platform == 'win32'", want: false},
		{expr: "implementation_name == 'cpython'", wantErr: "implementation_name"},
		{expr: "python_version ~= '3.10'", wantErr: "~="},
		{expr: "sys_platform < 'linux'", wantErr: "sys_platform"},
		{expr: "sys_platform == 'linux' and", wantErr: "ends"},
		{expr: "sys_platform == 'linux' )", wantErr: ")"},
		{expr: "sys_platform == 'linux", wantErr: "quote"},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			t.Parallel()
			got, err := EvalMarker(tt.expr, linux315)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("EvalMarker(%q) = %t, %v; want an error naming %q", tt.expr, got, err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("EvalMarker(%q) = %t, %v; want %t", tt.expr, got, err, tt.want)
			}
		})
	}
}

// TestEvalMarkerDist judges a released package's Requires-Dist markers as
// pip check does, on each architecture the image builds for.
func TestEvalMarkerDist(t *testing.T) {
	t.Parallel()
	envs := DistEnvs("3.15.2")
	if len(envs) != 2 {
		t.Fatalf("DistEnvs gave %d environments; want one each for x86_64 and aarch64", len(envs))
	}
	arch := map[string]MarkerEnv{}
	for _, e := range envs {
		arch[e.Dist.PlatformMachine] = e
	}
	tests := []struct {
		expr, machine string
		want          bool
		wantErr       string
	}{
		// pip check reads no extra
		{expr: `extra == "socks"`, machine: "x86_64", want: false},
		{expr: `extra != "socks"`, machine: "x86_64", want: true},
		{expr: `python_version >= "3.15"`, machine: "x86_64", want: true},
		{expr: `python_full_version >= "3.15.1"`, machine: "x86_64", want: true},
		{expr: `python_full_version < "3.15.2"`, machine: "aarch64", want: false},
		{expr: `os_name == "nt"`, machine: "x86_64", want: false},
		{expr: `implementation_name == "cpython"`, machine: "x86_64", want: true},
		{expr: `platform_python_implementation != "PyPy"`, machine: "x86_64", want: true},
		{expr: `sys_platform == "linux" and platform_system == "Linux"`, machine: "x86_64", want: true},
		{expr: `platform_machine == "aarch64"`, machine: "aarch64", want: true},
		{expr: `platform_machine == "aarch64"`, machine: "x86_64", want: false},
		{expr: `platform_release == "6.1"`, machine: "x86_64", wantErr: "platform_release"},
	}
	for _, tt := range tests {
		t.Run(tt.machine+" "+tt.expr, func(t *testing.T) {
			t.Parallel()
			env, ok := arch[tt.machine]
			if !ok {
				t.Fatalf("DistEnvs gave no environment for %s", tt.machine)
			}
			got, err := EvalMarker(tt.expr, env)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("EvalMarker(%q) = %t, %v; want an error naming %q", tt.expr, got, err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("EvalMarker(%q) = %t, %v; want %t", tt.expr, got, err, tt.want)
			}
		})
	}
}

// TestEvalMarkerRequirementsUnchanged: a requirements file is still read
// for the three variables it names, and nothing it cannot know.
func TestEvalMarkerRequirementsUnchanged(t *testing.T) {
	t.Parallel()
	for _, expr := range []string{`platform_machine == "x86_64"`, `extra == "socks"`, `os_name == "posix"`} {
		if got, err := EvalMarker(expr, linux315); err == nil {
			t.Errorf("EvalMarker(%q) under the requirements' platform = %t, nil; want an error", expr, got)
		}
	}
}
