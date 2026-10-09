// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/home-mandate/ha-home-mandate/internal/config"
)

// permissionHint adds the fix to an error from a file this process may not open. In
// container mode Home-Mandate runs unprivileged: the data directory belongs to its user,
// the token file, the certificate and the key must be readable by it (README, "Running
// in container mode"). Other errors are returned unchanged.
func permissionHint(err error, uid, gid int) error {
	if !errors.Is(err, fs.ErrPermission) {
		return err
	}
	return fmt.Errorf("%w; Home-Mandate runs as uid %d (gid %d): make the file readable by this user, "+
		"e.g. chown %d:%d on the host file, and the data directory its own "+
		`(README, "Running in container mode")`, err, uid, gid, uid, gid)
}

// rootWarning is the warning for a container started as root; in app mode the
// Supervisor runs the image as root and creates the files of /data and /ssl for root.
func rootWarning(mode config.Mode, uid int) string {
	if mode != config.ModeContainer || uid != 0 {
		return ""
	}
	return `running as root; container mode needs no privileges, run it as an unprivileged user ` +
		`(README, "Running in container mode")`
}
