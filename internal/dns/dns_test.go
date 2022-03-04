package dns

import (
	"os"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func TestGetCacheEntry(t *testing.T) {
	t.Parallel()

	// test expire
	logger := logrus.New()
	logger.SetOutput(os.Stdout)
	logger.SetLevel(logrus.DebugLevel)

	dns := NewCachedDNSResolver("8.8.8.8:53", 1*time.Second, 10*time.Second, 1*time.Microsecond, logger)
	dns.updateCache("1.1.1.1", []string{"asdf.com", "ghjkl.com"})
	time.Sleep(1 * time.Microsecond)
	res := dns.getCacheEntry("1.1.1.1")
	if res != nil {
		t.Fatalf("cache not expired: %v", res)
	}

	dns = NewCachedDNSResolver("8.8.8.8:53", 1*time.Second, 10*time.Second, 1*time.Hour, logger)
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
