package analyze

import "testing"

func levels(n int, f func(i int) uint8) Wave {
	w := Wave{Level: make([]uint8, n)}
	for i := range w.Level {
		w.Level[i] = f(i)
	}
	return w
}

func TestEnergyRisesWithLoudnessAndSustain(t *testing.T) {
	flat := func(v uint8) Wave { return levels(100, func(int) uint8 { return v }) }
	calm := energyOf(-20, flat(40))
	mid := energyOf(-12, levels(100, func(i int) uint8 { return uint8(100 + (i%2)*100) }))
	full := energyOf(-5, flat(255))
	if !(calm < mid && mid < full) {
		t.Errorf("energies %v %v %v should rise", calm, mid, full)
	}
	if calm != 0 || full != 1 {
		t.Errorf("the ends are 0 and 1, got %v and %v", calm, full)
	}
}

func TestEnergyBreakdownsLowerIt(t *testing.T) {
	steady := levels(100, func(int) uint8 { return 200 })
	breaks := levels(100, func(i int) uint8 {
		if i%2 == 0 {
			return 200
		}
		return 30
	})
	if energyOf(-9, breaks) >= energyOf(-9, steady) {
		t.Error("a song that is half breakdown should have less energy than one that never lets up")
	}
}

func TestEnergyUnknownIsZero(t *testing.T) {
	if energyOf(0, Wave{}) != 0 {
		t.Error("nothing known should be 0")
	}
}
