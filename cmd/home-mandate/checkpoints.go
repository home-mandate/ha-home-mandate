// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/home-mandate/home-mandate/internal/approval"
	"github.com/home-mandate/home-mandate/internal/audit"
	"github.com/home-mandate/home-mandate/internal/ha"
	"github.com/home-mandate/home-mandate/internal/i18n"
)

const (
	// auditKeyFile holds the key that signs the checkpoints of the audit log, next to
	// the database unless envAuditKeyFile names another place.
	auditKeyFile    = "audit-checkpoint.key"
	envAuditKeyFile = "HM_AUDIT_KEY_FILE"
	// Settings: the identifier of this audit log and when its position last left the device.
	settingLogID    = "audit_log_id"
	settingAnchored = "audit_anchored_at"

	checkpointInterval = 15 * time.Minute
	// anchorInterval is how often a checkpoint is also sent to the approvers: often
	// enough to bound what can be rewritten unnoticed, rare enough not to be noise.
	anchorInterval = 24 * time.Hour
	digestShown    = 16
)

// settings is what the checkpoints keep in the database.
type settings interface {
	Setting(ctx context.Context, key string) (string, bool, error)
	SetSetting(ctx context.Context, key, value string) error
}

// loadSigner returns the signer for the checkpoints of the audit log (SPEC-v0 section
// 9.5). The key is created on first use. It lies in the data directory by default: that
// protects a copy of the database alone, not against someone who can read the whole
// directory. HM_AUDIT_KEY_FILE names a place outside of it.
func loadSigner(ctx context.Context, st settings, dataDir string, getenv func(string) string) (*audit.Signer, error) {
	path := getenv(envAuditKeyFile)
	if path == "" {
		path = filepath.Join(dataDir, auditKeyFile)
	}
	key, err := readOrCreateKey(path)
	if err != nil {
		return nil, fmt.Errorf("audit checkpoint key %s: %w", path, err)
	}
	logID, found, err := st.Setting(ctx, settingLogID)
	if err != nil {
		return nil, err
	}
	if !found {
		logID = newUUID()
		if err := st.SetSetting(ctx, settingLogID, logID); err != nil {
			return nil, err
		}
	}
	sum := sha256.Sum256(key.Public().(ed25519.PublicKey))
	return &audit.Signer{LogID: logID, KeyID: "log-" + hex.EncodeToString(sum[:8]), Key: key}, nil
}

// readOrCreateKey reads the Ed25519 seed from path, or creates the file, readable by
// its owner only.
func readOrCreateKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		seed := make([]byte, ed25519.SeedSize)
		_, _ = rand.Read(seed) // crypto/rand.Read never fails (Go ≥ 1.24)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, err
		}
		if _, err := f.WriteString(base64.StdEncoding.EncodeToString(seed) + "\n"); err != nil {
			_ = f.Close()
			return nil, err
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
		return ed25519.NewKeyFromSeed(seed), nil
	}
	if err != nil {
		return nil, err
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("not an Ed25519 seed in base64")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// newUUID returns a random UUID (version 4) in lower case.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// anchor takes the position of the audit log to a place outside the device, where
// someone who later rewrites the log, even with the signing key, cannot take it back.
type anchor interface {
	Publish(ctx context.Context, pos audit.Position) error
}

// approverAnchor sends the position to the devices of the approvers. A later witness
// in the cloud takes the same place.
type approverAnchor struct {
	approvers interface {
		List(ctx context.Context) ([]approval.Approver, error)
	}
	notify   func(ctx context.Context, service string, n ha.Notification) error
	language func() i18n.Lang
}

// errNoApprover means nobody could be told the position.
var errNoApprover = errors.New("no approver device reached")

func (a approverAnchor) Publish(ctx context.Context, pos audit.Position) error {
	approvers, err := a.approvers.List(ctx)
	if err != nil {
		return err
	}
	reached := 0
	for _, ap := range approvers {
		lang := a.language()
		if l, ok := i18n.Parse(ap.Language); ok {
			lang = l
		}
		n := ha.Notification{
			Title: i18n.T(lang, i18n.CheckpointTitle, nil),
			Message: i18n.T(lang, i18n.CheckpointMessage, i18n.Args{
				"seq": fmt.Sprint(pos.Seq), "digest": shortDigest(pos.Digest), "log": pos.LogID}),
		}
		for _, d := range ap.Devices {
			if a.notify(ctx, d.Service, n) == nil {
				reached++
			}
		}
	}
	if reached == 0 {
		return errNoApprover
	}
	return nil
}

// shortDigest is the start of a digest, enough to compare by eye.
func shortDigest(digest string) string {
	hexPart := strings.TrimPrefix(digest, "sha256:")
	if len(hexPart) > digestShown {
		hexPart = hexPart[:digestShown]
	}
	return hexPart
}

// checkpointer writes checkpoints and takes them outside once per anchorInterval.
type checkpointer struct {
	log      *audit.Log
	settings settings
	anchor   anchor
	now      func() time.Time
	logger   *slog.Logger
}

// once writes a checkpoint if the log has new entries and publishes its position if
// the last one left the device long enough ago.
func (c checkpointer) once(ctx context.Context) {
	pos, err := c.log.Checkpoint(ctx)
	if err != nil {
		c.logger.Error("audit log checkpoint failed", "error", err)
		return
	}
	if pos == (audit.Position{}) || c.anchor == nil {
		return
	}
	last, _, err := c.settings.Setting(ctx, settingAnchored)
	if err != nil {
		c.logger.Error("cannot read when the audit log was last anchored", "error", err)
		return
	}
	if at, err := time.Parse(time.RFC3339, last); err == nil && c.now().Sub(at) < anchorInterval {
		return
	}
	if err := c.anchor.Publish(ctx, pos); err != nil {
		c.logger.Warn("audit log position not sent outside the device", "error", err)
		return
	}
	if err := c.settings.SetSetting(ctx, settingAnchored, c.now().UTC().Format(time.RFC3339)); err != nil {
		c.logger.Error("cannot store when the audit log was anchored", "error", err)
	}
}

// run writes checkpoints at start and then every checkpointInterval until ctx ends.
func (c checkpointer) run(ctx context.Context) {
	ticker := time.NewTicker(checkpointInterval)
	defer ticker.Stop()
	for {
		c.once(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
