package gh

import "testing"

func TestParse(t *testing.T) {
	pr := parse([]byte(`[{"number":75,"state":"MERGED","url":"https://github.com/o/r/pull/75","headRefOid":"abc"}]`))
	if pr == nil || pr.Number != 75 || pr.State != "MERGED" || pr.HeadOid != "abc" {
		t.Errorf("parse = %+v", pr)
	}
	for _, out := range []string{`[]`, `not json`, ``} {
		if pr := parse([]byte(out)); pr != nil {
			t.Errorf("parse(%q) = %+v, want nil", out, pr)
		}
	}
}
