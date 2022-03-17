package dns

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
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
	logger       *logrus.Logger
}

func NewCachedDNSResolver(ctx context.Context, server string, connectTimeout, timeout time.Duration, cacheTimeout time.Duration, logger *logrus.Logger) *CachedDNSResolver {
	resolver := net.DefaultResolver
	if server != "" {
		resolver = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
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
		logger:       logger,
	}
}

// CachedDNSLookup performs a DNS lookup and caches the result to
// not hammer your DNS server.
func (r *CachedDNSResolver) CachedDNSLookup(ip string) ([]string, error) {
	r.logger.Debugf("resolving %s", ip)
	val := r.getCacheEntry(ip)
	if val != nil {
		return val, nil
	}

	ctx, cancel := context.WithTimeout(r.ctx, r.timeout)
	defer cancel()

	domains, err := r.resolver.LookupAddr(ctx, ip)
	if err != nil {
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
	entry := cacheEntry{
		domains:   domains,
		timestamp: time.Now(),
	}
	r.dnsCache[ip] = entry
}

func (r *CachedDNSResolver) getCacheEntry(ip string) []string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if val, ok := r.dnsCache[ip]; ok {
		// check if the cache expired
		if time.Now().Add(-1 * r.cacheTimeout).After(val.timestamp) {
			// cache expired, remove the entry
			r.logger.Debugf("deleting stale DNS entry for %s, Store Time: %s", ip, val.timestamp)
			delete(r.dnsCache, ip)
			return nil
		}
		return val.domains
	}
	return nil
}
