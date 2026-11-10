package titlecase

import "testing"

func TestTitleCaseEveryWord(t *testing.T) {
	if got := TitleCase("hello world"); got != "Hello World" {
		t.Fatalf("TitleCase(\"hello world\") = %q, want \"Hello World\"", got)
	}
}
