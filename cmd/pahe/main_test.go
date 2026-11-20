package main

import "testing"

func TestParseSelection(t *testing.T) {
	sel, err := parseSelection("1-3, 5, 12.5")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"1", "2", "3", "5", "12.5"} {
		if !sel(n) {
			t.Errorf("%s should be selected", n)
		}
	}
	for _, n := range []string{"4", "6", "12", "abc"} {
		if sel(n) {
			t.Errorf("%s should not be selected", n)
		}
	}
	if _, err := parseSelection("5-2"); err == nil {
		t.Error("reversed range should fail")
	}
	all, _ := parseSelection("all")
	if !all("999") {
		t.Error("all should select everything")
	}
}
