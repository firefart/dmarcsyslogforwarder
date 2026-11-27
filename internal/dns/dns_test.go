package dns

import (
	"log/slog"
	"testing"
	"time"
)

func TestGetCacheEntry(t *testing.T) {
	t.Parallel()

	// test expire
	logger := slog.New(slog.DiscardHandler)

	dns := NewCachedDNSResolver(t.Context(), "8.8.8.8:53", 1*time.Second, 10*time.Second, 1*time.Microsecond, logger)
	dns.updateCache("1.1.1.1", []string{"asdf.com", "ghjkl.com"})
	time.Sleep(1 * time.Microsecond)
	res := dns.getCacheEntry("1.1.1.1")
	if res != nil {
		t.Fatalf("cache not expired: %v", res)
	}

	dns = NewCachedDNSResolver(t.Context(), "8.8.8.8:53", 1*time.Second, 10*time.Second, 1*time.Hour, logger)
	dns.updateCache("1.1.1.1", []string{"asdf.com", "ghjkl.com"})
	res = dns.getCacheEntry("1.1.1.1")
	if res == nil {
		t.Fatal("cache expired and should not be")
	}
	if len(res) != 2 {
		t.Fatalf("wrong cache size returned: %d", len(res))
	}
	if res[0] != "asdf.com" && res[1] != "ghjkl.com" {
		t.Fatalf("wrong domains returned, got %v", res)
	}
}

func TestCacheSweep(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	dns := NewCachedDNSResolver(t.Context(), "", 1*time.Second, 10*time.Second, 1*time.Hour, logger)
	dns.updateCache("1.1.1.1", []string{"one.one.one.one"})
	dns.updateCache("8.8.8.8", []string{"dns.google"})

	// make the first entry and the last sweep look old
	old := time.Now().Add(-2 * time.Hour)
	dns.dnsCache["1.1.1.1"] = cacheEntry{domains: []string{"one.one.one.one"}, timestamp: old}
	dns.lastSweep = old

	dns.updateCache("9.9.9.9", []string{"dns.quad9.net"})

	if _, ok := dns.dnsCache["1.1.1.1"]; ok {
		t.Fatal("expired entry was not swept")
	}
	if len(dns.dnsCache) != 2 {
		t.Fatalf("expected 2 entries after sweep, got %d", len(dns.dnsCache))
	}
}
