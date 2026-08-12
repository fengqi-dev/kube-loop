package service

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"strings"
	"sync"
	"time"
)

const loginLimitWindow = time.Minute

type limitBucket struct {
	started time.Time
	count   int
}

type loginLimiter struct {
	mu       sync.Mutex
	accounts map[string]limitBucket
	clients  map[string]limitBucket
	now      func() time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{
		accounts: make(map[string]limitBucket), clients: make(map[string]limitBucket), now: time.Now,
	}
}

func (limiter *loginLimiter) allow(providerID, subject, remoteAddress string) bool {
	now := limiter.now().UTC()
	accountDigest := sha256.Sum256([]byte(providerID + "\x00" + strings.ToLower(strings.TrimSpace(subject))))
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

func (limiter *loginLimiter) success(providerID, subject string) {
	digest := sha256.Sum256([]byte(providerID + "\x00" + strings.ToLower(strings.TrimSpace(subject))))
	limiter.mu.Lock()
	delete(limiter.accounts, hex.EncodeToString(digest[:]))
	limiter.mu.Unlock()
}

func incrementBucket(bucket limitBucket, now time.Time, maximum int) (limitBucket, bool) {
	if bucket.started.IsZero() || now.Sub(bucket.started) >= loginLimitWindow {
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
