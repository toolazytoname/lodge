package agent

import "testing"

func TestBoundedBufferRetainsPrefixWithoutUnboundedGrowth(t *testing.T) {
	buffer := boundedBuffer{limit: 5}
	if written, err := buffer.Write([]byte("abc")); err != nil || written != 3 {
		t.Fatalf("first write failed: written=%d err=%v", written, err)
	}
	if written, err := buffer.Write([]byte("defg")); err != nil || written != 4 {
		t.Fatalf("second write failed: written=%d err=%v", written, err)
	}
	if got := buffer.String(); got != "abcde" || !buffer.exceeded {
		t.Fatalf("bounded buffer state is wrong: content=%q exceeded=%v", got, buffer.exceeded)
	}
	if written, err := buffer.Write([]byte("ignored")); err != nil || written != 7 || buffer.String() != "abcde" {
		t.Fatalf("overflow write should be consumed without growth: written=%d err=%v content=%q", written, err, buffer.String())
	}
}
