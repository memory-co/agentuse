package device

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/memory-co/agentuse/internal/securews"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/database"
	"github.com/memory-co/agentuse/server/internal/notification"
)

func (s *Service) Enroll(ctx context.Context, token, hostname string, csr []byte) (agentuse.Device, []byte, error) {
	tx, err := database.Begin(ctx, s.DB)
	if err != nil {
		return agentuse.Device{}, nil, err
	}
	defer tx.Rollback()
	var stored database.Device
	if err = tx.WithContext(ctx).Where("setup_token=?", token).Take(&stored).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = database.ErrNotFound
		}
		return agentuse.Device{}, nil, err
	}
	raw, err := s.Identity.Issue(csr, stored.ID, stored.CredentialGeneration, s.Now())
	if err != nil {
		return agentuse.Device{}, nil, err
	}
	cert, err := securews.VerifyDevice(s.Identity.CA, raw, s.Now())
	if err != nil {
		return agentuse.Device{}, nil, err
	}
	if err = s.recordCertificate(ctx, tx, cert); err != nil {
		return agentuse.Device{}, nil, err
	}
	name := string(stored.Name)
	if name == "" {
		name = strings.TrimSpace(hostname)
	}
	if name == "" {
		name = stored.ID
	}
	if err = tx.WithContext(ctx).Model(&database.Device{}).Where("id=? AND setup_token=?", stored.ID, token).Updates(map[string]any{"name": name, "setup_token": nil, "key_hash": nil, "status": agentuse.DeviceOffline}).Error; err != nil {
		return agentuse.Device{}, nil, err
	}
	if err = tx.Commit().Error; err != nil {
		return agentuse.Device{}, nil, err
	}
	d, err := s.GetDevice(ctx, agentuse.User{Role: agentuse.RoleAdmin}, stored.ID)
	if err == nil {
		s.Changes.Publish(notification.Change{Resource: "devices", ID: d.ID}, d.UserID)
	}
	return d, raw, err
}

func (s *Service) recordCertificate(ctx context.Context, tx *gorm.DB, p securews.Peer) error {
	return tx.WithContext(ctx).Create(&database.DeviceCertificate{Serial: p.Serial, DeviceID: p.DeviceID, Generation: p.Generation, KeyHash: p.KeyHash, NotBefore: p.Certificate.NotBefore.Unix(), NotAfter: p.NotAfter.Unix()}).Error
}

func (s *Service) AuthenticateCertificate(ctx context.Context, p securews.Peer) (agentuse.Device, error) {
	var count int64
	err := s.DB.WithContext(ctx).Table("device_certificates c").Joins("JOIN devices d ON d.id=c.device_id AND d.credential_generation=c.generation").Where("c.serial=? AND c.device_id=? AND c.generation=? AND c.key_hash=? AND c.revoked_at IS NULL AND c.not_before<=? AND c.not_after>? AND d.setup_token IS NULL", p.Serial, p.DeviceID, p.Generation, p.KeyHash, s.Now().Unix(), s.Now().Unix()).Count(&count).Error
	if err != nil {
		return agentuse.Device{}, err
	}
	if count != 1 {
		return agentuse.Device{}, fmt.Errorf("device credential is revoked or expired")
	}
	return s.GetDevice(ctx, agentuse.User{Role: agentuse.RoleAdmin}, p.DeviceID)
}

func (s *Service) RenewCertificate(ctx context.Context, p securews.Peer, csr []byte) ([]byte, error) {
	tx, err := database.Begin(ctx, s.DB)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Check the current generation and serial inside the same transaction.
	var count int64
	if err = tx.WithContext(ctx).Table("device_certificates c").Joins("JOIN devices d ON d.id=c.device_id AND d.credential_generation=c.generation").Where("c.serial=? AND c.device_id=? AND c.revoked_at IS NULL AND c.not_after>?", p.Serial, p.DeviceID, s.Now().Unix()).Count(&count).Error; err != nil || count != 1 {
		return nil, fmt.Errorf("device credential cannot renew")
	}
	raw, err := s.Identity.Issue(csr, p.DeviceID, p.Generation, s.Now())
	if err != nil {
		return nil, err
	}
	cert, err := securews.VerifyDevice(s.Identity.CA, raw, s.Now())
	if err != nil {
		return nil, err
	}
	if cert.KeyHash != p.KeyHash {
		return nil, fmt.Errorf("renewal must prove the existing device key")
	}
	if err = s.recordCertificate(ctx, tx, cert); err != nil {
		return nil, err
	}
	// Keep the old certificate valid until its original expiry. A lost renewal
	// reply must not strand the device before its new material is atomically saved.

	return raw, tx.Commit().Error
}

func (s *Service) revokeCertificates(ctx context.Context, tx *gorm.DB, device string) error {
	if err := tx.WithContext(ctx).Model(&database.DeviceCertificate{}).Where("device_id=? AND revoked_at IS NULL", device).Update("revoked_at", s.Now().Unix()).Error; err != nil {
		return err
	}
	return tx.WithContext(ctx).Model(&database.Device{}).Where("id=?", device).Update("credential_generation", gorm.Expr("credential_generation+1")).Error
}
