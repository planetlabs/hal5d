package subscriber

import (
	"testing"

	"github.com/negz/hal5d/internal/cert"
)

func TestSubscriber(t *testing.T) {
	var _ cert.Subscriber = &Subscriber{}
}
