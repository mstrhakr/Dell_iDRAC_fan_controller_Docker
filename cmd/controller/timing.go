// SPDX-FileCopyrightText: 2020-2026 Tigerblue77 and the Dell iDRAC fan controller Docker image contributors
// SPDX-License-Identifier: AGPL-3.0-only

package main

import "time"

// observationGapLimit tolerates polling jitter but never interprets a prolonged
// blind period as evidence of stable thermal operation.
func observationGapLimit(interval time.Duration) time.Duration {
	return max(90*time.Second, 3*interval)
}
