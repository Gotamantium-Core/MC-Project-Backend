package main

import "testing"

func TestAdvanceHead(t *testing.T) {
	q := []Token{{ID: "105"}, {ID: "106"}}
	q, ok := advance(q, "105")
	if !ok || len(q) != 1 || q[0].ID != "106" {
		t.Fatalf("got %v %v", q, ok)
	}
}

func TestAdvanceMismatchKeepsQueue(t *testing.T) {
	q := []Token{{ID: "105"}}
	if _, ok := advance(q, "999"); ok {
		t.Fatal("mismatch advanced")
	}
	if _, ok := advance(nil, "105"); ok {
		t.Fatal("empty advanced")
	}
}

func TestFormats(t *testing.T) {
	if got := fmtA([]Token{{ID: "105"}, {ID: "106"}}); got != "105 Q1" {
		t.Fatal(got)
	}
	if got := fmtA(nil); got != "IDLE Q0" {
		t.Fatal(got)
	}
	if got := fmtK([]Token{{ID: "106", Code: "POR"}, {ID: "107", Code: "CHK"}}); got != "106 POR Q1" {
		t.Fatal(got)
	}
	if got := fmtR([]Token{{ID: "108", Code: "POR"}}); got != "108 POR W0" {
		t.Fatal(got)
	}
	if got := fmtR(nil); got != "NONE W0" {
		t.Fatal(got)
	}
}
