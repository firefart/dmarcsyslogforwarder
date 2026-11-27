package dns

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"
)

type cacheEntry struct {
	domains   []string
	timestamp time.Time
}

type CachedDNSResolver struct {
	ctx          context.Context
	timeout      time.Duration
	cacheTimeout time.Duration
	resolver     *net.Resolver
	mutex        sync.RWMutex
	dnsCache     map[string]cacheEntry
	lastSweep    time.Time
	logger       *slog.Logger
}

func NewCachedDNSResolver(ctx context.Context, server string, connectTimeout, timeout time.Duration, cacheTimeout time.Duration, logger *slog.Logger) *CachedDNSResolver {
	resolver := net.DefaultResolver
	if server != "" {
		resolver = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				d := net.Dialer{
					Timeout: connectTimeout,
				}
				return d.DialContext(ctx, network, server)
			},
		}
	}
	return &CachedDNSResolver{
		ctx:          ctx,
		timeout:      timeout,
		cacheTimeout: cacheTimeout,
		resolver:     resolver,
		dnsCache:     make(map[string]cacheEntry),
		lastSweep:    time.Now(),
		logger:       logger,
	}
}

// CachedDNSLookup performs a DNS lookup and caches the result to
// not hammer your DNS server.
func (r *CachedDNSResolver) CachedDNSLookup(ip string) ([]string, error) {
	r.logger.Debug("resolving dns", slog.String("ip", ip))
	val := r.getCacheEntry(ip)
	if val != nil {
		return val, nil
	}

	ctx, cancel := context.WithTimeout(r.ctx, r.timeout)
	defer cancel()

	domains, err := r.resolver.LookupAddr(ctx, ip)
	if err != nil {
		// do not cache failures caused by the program shutting down
		if r.ctx.Err() != nil {
			return nil, err
		}
		// store dummy entry so we do not reresolve the ip
		r.updateCache(ip, []string{})
		return nil, err
	}

	// remove trailing dot from domains
	for i := range domains {
		domains[i] = strings.TrimSuffix(domains[i], ".")
	}
	r.updateCache(ip, domains)
	return domains, nil
}

func (r *CachedDNSResolver) updateCache(ip string, domains []string) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	now := time.Now()
	r.sweep(now)
	entry := cacheEntry{
		domains:   domains,
		timestamp: now,
	}
	r.dnsCache[ip] = entry
}

// sweep removes all expired entries from the cache so it does not grow
// forever. It runs at most once per cache timeout. The caller must hold the lock.
func (r *CachedDNSResolver) sweep(now time.Time) {
	if now.Sub(r.lastSweep) < r.cacheTimeout {
		return
	}
	r.lastSweep = now
	for ip, entry := range r.dnsCache {
		if r.isExpired(entry, now) {
			delete(r.dnsCache, ip)
		}
	}
}

func (r *CachedDNSResolver) isExpired(entry cacheEntry, now time.Time) bool {
	return now.Sub(entry.timestamp) > r.cacheTimeout
}

func (r *CachedDNSResolver) getCacheEntry(ip string) []string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if val, ok := r.dnsCache[ip]; ok {
		// check if the cache expired
		if r.isExpired(val, time.Now()) {
			// cache expired, remove the entry
			r.logger.Debug("deleting stale DNS entry", slog.String("ip", ip), slog.Time("store-time", val.timestamp))
			delete(r.dnsCache, ip)
			return nil
		}
		return val.domains
	}
	return nil
}
