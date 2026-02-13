package temporal

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestWorkflowBlackbox(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Workflow Blackbox Suite")
}
