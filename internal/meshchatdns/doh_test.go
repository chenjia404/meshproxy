package meshchatdns

import (
	"testing"
	"time"
)

func TestDecodeAnswerIPsA(t *testing.T) {
	body := []byte(`{
		"Status": 0,
		"Answer": [
			{"name":"example.com","type":1,"TTL":120,"data":"1.2.3.4"},
			{"name":"example.com","type":1,"TTL":60,"data":"5.6.7.8"}
		]
	}`)
	ips, ttl, err := decodeAnswerIPs(body, "A")
	if err != nil {
		t.Fatalf("decodeAnswerIPs(A) error = %v", err)
	}
	if len(ips) != 2 {
		t.Fatalf("ip count = %d, want 2", len(ips))
	}
	if ips[0].String() != "1.2.3.4" || ips[1].String() != "5.6.7.8" {
		t.Fatalf("ips = %v", ips)
	}
	if ttl != time.Minute {
		t.Fatalf("ttl = %v, want 1m", ttl)
	}
}

func TestDecodeAnswerIPsAAAA(t *testing.T) {
	body := []byte(`{
		"Status": 0,
		"Answer": [
			{"name":"example.com","type":28,"TTL":180,"data":"2400:3200::1"},
			{"name":"example.com","type":1,"TTL":60,"data":"1.2.3.4"}
		]
	}`)
	ips, ttl, err := decodeAnswerIPs(body, "AAAA")
	if err != nil {
		t.Fatalf("decodeAnswerIPs(AAAA) error = %v", err)
	}
	if len(ips) != 1 || ips[0].String() != "2400:3200::1" {
		t.Fatalf("ips = %v, want [2400:3200::1]", ips)
	}
	if ttl != 3*time.Minute {
		t.Fatalf("ttl = %v, want 3m", ttl)
	}
}
