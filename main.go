package main

import (
	"github.com/penny-vault/accelerating-dual-momentum/adm"
	"github.com/penny-vault/pvbt/cli"
)

func main() {
	cli.Run(&adm.AcceleratingDualMomentum{})
}
