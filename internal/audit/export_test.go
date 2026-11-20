// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

// WriteVersion0 makes l write entries of audit/v0, as Home-Mandate up to 0.1 did, so that a
// test can build a log that moves to version 1.
func WriteVersion0(l *Log) { l.version0 = true }
