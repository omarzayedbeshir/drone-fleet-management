package auth

import (
	"golang.org/x/crypto/bcrypt"
	"strings"
	"testing"
)

func TestPasswordHashing(t *testing.T) {
	hash, err := HashPassword("a sufficiently long password")
	if err != nil {
		t.Fatal(err)
	}
	if cost, err := bcrypt.Cost([]byte(hash)); err != nil || cost != 12 {
		t.Fatal("incorrect bcrypt cost")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("a sufficiently long password")) != nil {
		t.Fatal("hash mismatch")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("wrong password")) == nil {
		t.Fatal("accepted wrong password")
	}
	for _, value := range []string{"short", strings.Repeat("a", 73), strings.Repeat("é", 37)} {
		if _, err := HashPassword(value); err == nil {
			t.Fatal("accepted invalid password length")
		}
	}
}
func TestEmailsAndTokens(t *testing.T) {
	email, err := NormalizeEmail(" Pilot@Example.COM ")
	if err != nil || email != "pilot@example.com" {
		t.Fatal("email normalization failed")
	}
	for _, value := range []string{"", "not-an-email", "Pilot <pilot@example.com>"} {
		if _, err := NormalizeEmail(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	first, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !ValidToken(first) || len(TokenHash(first)) != 32 || ValidToken("invalid") {
		t.Fatal("invalid token handling")
	}
}
