// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build race

package audit_test

// raceDetector: the race detector slows the pure-Go SQLite down about 40 times, so the
// load tests use a tenth of their size; the query plan does not depend on it.
const raceDetector = true
