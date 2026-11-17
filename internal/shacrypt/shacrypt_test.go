// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package shacrypt

import (
	"errors"
	"testing"
)

// Vectors from Drepper's specification, checked against openssl passwd and
// mkpasswd.
var vectors = []struct {
	fn             func(string, string, int) string
	password, salt string
	rounds         int
	want           string
}{
	{Crypt512, "Hello world!", "saltstring", 0,
		"$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1"},
	{Crypt512, "Hello world!", "saltstringsaltstring", 10000,
		"$6$rounds=10000$saltstringsaltst$OW1/O6BYHV6BcXZu8QVeXbDWra3Oeqh0sbHbbMCVNSnCM/UrjmM0Dp8vOuZeHBy/YTBmSK6H9qs/y3RnOaw5v."},
	{Crypt512, "This is just a test", "toolongsaltstring", 5000,
		"$6$rounds=5000$toolongsaltstrin$lQ8jolhgVRVhY4b5pZKaysCLi0QBxGoNeKQzQ3glMhwllF7oGDZxUhx1yxdYcz/e1JSbq3y6JMxxl8audkUEm0"},
	{Crypt512, "the minimum number is still observed", "roundstoolow", 10,
		"$6$rounds=1000$roundstoolow$kUMsbe306n21p9R.FRkW3IGn.S9NPN0x50YhH1xhLsPuWGsUSklZt58jaTfF4ZEQpyUNGc0dqbpBYYBaHHrsX."},
	{Crypt512, "correct-horse", "fakesalt", 0,
		"$6$fakesalt$J4U.T0okq1KkYgAckSa/CjjdXnuFjKbuvq8yt6VcbwTEsbDayuIqoNXNtgVK53XnCLu3YiWHZPNNJ8kqw1y1/0"},
	{Crypt256, "Hello world!", "saltstring", 0,
		"$5$saltstring$5B8vYYiY.CVt1RlTTf8KbXBH3hsxY/GNooZaBBGWEc5"},
	{Crypt256, "p@ss", "abcdefgh", 1000,
		"$5$rounds=1000$abcdefgh$2c9DAT/89Ja9xKowu/YxyKQj2e6cdcJA8FQ/se7MkQ2"},
	{Crypt256, "Hello world!", "saltstringsaltstring", 10000,
		"$5$rounds=10000$saltstringsaltst$3xv.VbSHBb41AL9AvLeujZkZRBAwqFMz2.opqey6IcA"},
}

func TestCrypt(t *testing.T) {
	t.Parallel()
	for _, v := range vectors {
		if got := v.fn(v.password, v.salt, v.rounds); got != v.want {
			t.Errorf("crypt(%q, %q, %d) = %q, want %q", v.password, v.salt, v.rounds, got, v.want)
		}
	}
}

func TestVerify(t *testing.T) {
	t.Parallel()
	for _, v := range vectors {
		ok, err := Verify(v.password, v.want)
		if err != nil || !ok {
			t.Errorf("Verify(%q, %q) = %v, %v", v.password, v.want, ok, err)
		}
		if ok, err := Verify(v.password+"x", v.want); err != nil || ok {
			t.Errorf("Verify with a wrong password = %v, %v", ok, err)
		}
	}
	for _, h := range []string{"", "plain", "$1$abc$def", "$6$nosaltend", "$6$rounds=x$s$h", "$6$rounds=5000"} {
		if ok, err := Verify("pw", h); ok || !errors.Is(err, ErrUnsupported) {
			t.Errorf("Verify(%q) = %v, %v", h, ok, err)
		}
	}
	// A well formed hash that is simply wrong.
	if ok, err := Verify("pw", "$6$other$hash"); ok || err != nil {
		t.Errorf("Verify with a foreign hash = %v, %v", ok, err)
	}
}
