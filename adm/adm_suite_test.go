package adm_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAcceleratingDualMomentum(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Accelerating Dual Momentum Suite")
}
