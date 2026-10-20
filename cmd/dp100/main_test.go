package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestArguments(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		wantErr bool
	}{
		{[]string{"-h"}, false},
		{[]string{"-invalid"}, true},
		{[]string{"unexpected"}, true},
		{[]string{"-device", "/nonexistent/dp100"}, true},
	} {
		var out, errOut bytes.Buffer
		err := run(context.Background(), tc.args, &out, &errOut)
		if (err != nil) != tc.wantErr {
			t.Fatalf("args=%v err=%v", tc.args, err)
		}
		if len(tc.args) == 1 && tc.args[0] == "-h" && !strings.Contains(errOut.String(), "-device") {
			t.Fatal("missing help")
		}
	}
}
