package service

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"strings"
	"sync"
	"time"
)

const passwordLimitWindow = time.Minute

type limitBucket struct {
	started time.Time
	count   int
}

type passwordLimiter struct {
	mu       sync.Mutex
	accounts map[string]limitBucket
	clients  map[string]limitBucket
	now      func() time.Time
}

func newPasswordLimiter() *passwordLimiter {
	return &passwordLimiter{
		accounts: make(map[string]limitBucket), clients: make(map[string]limitBucket), now: time.Now,
	}
}

func (limiter *passwordLimiter) allow(providerID, username, remoteAddress string) bool {
	now := limiter.now().UTC()
	accountDigest := sha256.Sum256([]byte(providerID + "\x00" + strings.ToLower(strings.TrimSpace(username))))
	accountKey := hex.EncodeToString(accountDigest[:])
	clientKey := clientAddress(remoteAddress)
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	account, accountAllowed := incrementBucket(limiter.accounts[accountKey], now, 5)
	client, clientAllowed := incrementBucket(limiter.clients[clientKey], now, 30)
	limiter.accounts[accountKey] = account
	limiter.clients[clientKey] = client
	return accountAllowed && clientAllowed
}

func (limiter *passwordLimiter) success(providerID, username string) {
	digest := sha256.Sum256([]byte(providerID + "\x00" + strings.ToLower(strings.TrimSpace(username))))
	limiter.mu.Lock()
	delete(limiter.accounts, hex.EncodeToString(digest[:]))
	limiter.mu.Unlock()
}

func incrementBucket(bucket limitBucket, now time.Time, maximum int) (limitBucket, bool) {
	if bucket.started.IsZero() || now.Sub(bucket.started) >= passwordLimitWindow {
		bucket = limitBucket{started: now}
	}
	if bucket.count >= maximum {
		return bucket, false
	}
	bucket.count++
	return bucket, true
}

func clientAddress(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err == nil && host != "" {
		return host
	}
	return remote
}
