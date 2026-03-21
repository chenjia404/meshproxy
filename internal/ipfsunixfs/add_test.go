package ipfsunixfs

import "testing"

func TestCidBuilderV1UnixFS(t *testing.T) {
	cases := []string{
		"",
		"sha2-256",
		"SHA2-256",
		"sha2-512",
	}
	for _, hashFunction := range cases {
		builder, err := CidBuilderV1UnixFS(hashFunction)
		if err != nil {
			t.Fatalf("CidBuilderV1UnixFS(%q) returned error: %v", hashFunction, err)
		}
		if builder == nil {
			t.Fatalf("CidBuilderV1UnixFS(%q) returned nil builder", hashFunction)
		}
	}

	if _, err := CidBuilderV1UnixFS("md5"); err == nil {
		t.Fatalf("CidBuilderV1UnixFS should reject unsupported hash function")
	}
}

func TestChunkSizeFromSpec(t *testing.T) {
	cases := []struct {
		name string
		spec string
		want int
	}{
		{name: "empty", spec: "", want: 1048576},
		{name: "valid", spec: "size-1048576", want: 1048576},
		{name: "invalid", spec: "size-bad", want: 1048576},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ChunkSizeFromSpec(tc.spec); got != tc.want {
				t.Fatalf("ChunkSizeFromSpec(%q) = %d, want %d", tc.spec, got, tc.want)
			}
		})
	}
}
