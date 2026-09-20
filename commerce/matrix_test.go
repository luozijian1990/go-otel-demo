package commerce

import (
	"encoding/json"
	"os"
	"testing"
)

func TestMatrixCoversEveryChoice(t *testing.T) {
	b, err := os.ReadFile("../scripts/commerce-matrix.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ ID, Business, Target, Action string }
	if err = json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range cases {
		k := c.Business + "/" + c.Target + "/" + c.Action
		if seen[k] {
			t.Fatal("duplicate", k)
		}
		seen[k] = true
	}
	count := 0
	for business, options := range choices {
		for _, c := range options {
			count++
			if !seen[business+"/"+c.Target+"/"+c.Action] {
				t.Fatal("uncovered choice", business, c)
			}
		}
	}
	if count != len(cases) {
		t.Fatal("matrix has obsolete cases")
	}
}
