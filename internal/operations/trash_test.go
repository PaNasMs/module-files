package operations

import "testing"

func TestTrashRejectsEveryStorageRoot(t *testing.T) {
	roots := []string{"/srv/data", "/home/alice", "/"}
	for _, path := range []string{"/", "/srv/data", "/srv/data/", "/home/alice", "/srv/data/sub/.."} {
		if _, err := trashBase(path, roots); err == nil {
			t.Fatalf("accepted root %s", path)
		}
	}
	if base, err := trashBase("/srv/data/docs", roots); err != nil || base != "/srv/data" {
		t.Fatal(base, err)
	}
}
