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
