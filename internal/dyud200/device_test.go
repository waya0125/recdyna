// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

package dyud200

import "testing"

func TestFirmwareVersionFlags(t *testing.T) {
	cases := []struct {
		fw          FirmwareVersion
		wantNewInit bool
		wantBypass  bool
	}{
		{FirmwareVersion{1, 0, 2, 2}, false, false},
		{FirmwareVersion{1, 0, 5, 1}, false, false},
		{FirmwareVersion{1, 0, 5, 2}, true, false},
		{FirmwareVersion{1, 0, 5, 3}, true, true},
		{FirmwareVersion{1, 0, 5, 4}, true, false},
	}
	for _, tc := range cases {
		if got := tc.fw.NeedsNewInit(); got != tc.wantNewInit {
			t.Errorf("FW %s NeedsNewInit() = %v, want %v", tc.fw, got, tc.wantNewInit)
		}
		if got := tc.fw.IsXorBypassed(); got != tc.wantBypass {
			t.Errorf("FW %s IsXorBypassed() = %v, want %v", tc.fw, got, tc.wantBypass)
		}
	}
}
