//go:build system

package system_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBlackboxSystem(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Blackbox System Suite")
}
