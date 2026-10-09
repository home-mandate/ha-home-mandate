// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// foreignInfo is a file of another user, which a test without root cannot create.
type foreignInfo struct{ uid uint32 }

func (foreignInfo) Name() string       { return "home-mandate.db" }
func (foreignInfo) Size() int64        { return 0 }
func (foreignInfo) Mode() fs.FileMode  { return 0o600 }
func (foreignInfo) ModTime() time.Time { return time.Time{} }
func (foreignInfo) IsDir() bool        { return false }
func (i foreignInfo) Sys() any         { return &syscall.Stat_t{Uid: i.uid, Nlink: 1} }

// An upgrade to an unprivileged container finds the data directory of a root container:
// the error names the owner, this process's user and the command that fixes it.
func TestCheckOwnerNamesTheFix(t *testing.T) {
	uid, gid := os.Getuid(), os.Getgid()
	other := uint32(uid + 1)
	if uid != 0 {
		other = 0 // root, as a data directory from before the unprivileged container
	}
	for _, path := range []string{"/data", "/data/home-mandate.db", "/data/home-mandate.db-wal"} {
		t.Run(path, func(t *testing.T) {
			err := checkOwner(path, "/data", foreignInfo{uid: other})
			if !errors.Is(err, ErrInsecurePermissions) {
				t.Fatalf("error = %v, want ErrInsecurePermissions", err)
			}
			for _, want := range []string{
				fmt.Sprintf("%s is owned by uid %d", path, other),
				fmt.Sprintf("runs as uid %d", uid),
				fmt.Sprintf("chown -R %d:%d /data", uid, gid),
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to contain %q", err, want)
				}
			}
		})
	}
}

func TestCheckOwnerAcceptsThisUser(t *testing.T) {
	if err := checkOwner("/data", "/data", foreignInfo{uid: uint32(os.Getuid())}); err != nil {
		t.Fatal(err)
	}
}
