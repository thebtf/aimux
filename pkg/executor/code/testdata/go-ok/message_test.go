package sample

import "testing"

func TestMessageIsPresent(t *testing.T) {
	if Message == "" {
		t.Fatal("Message is empty")
	}
}
