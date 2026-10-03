package conic

import (
	"fmt"
	"testing"
)

type benchCfg struct {
	Host    string `mapstructure:"host" default:"localhost" env:"CONIC_BENCH_HOST"`
	Port    int    `mapstructure:"port" default:"80" env:"CONIC_BENCH_PORT"`
	Debug   bool   `mapstructure:"debug" default:"false"`
	Name    string `mapstructure:"name" env:"CONIC_BENCH_NAME_UNSET"`
	Workers int    `mapstructure:"workers" default:"4"`
}

// newBenchConic builds an instance whose file layer holds about 10k leaves
// nested five levels deep (big.aI.bJ.cK.dL) and binds a tagged struct.
func newBenchConic(b testing.TB) *Conic {
	c := New()
	big := map[string]any{}
	for a := 0; a < 10; a++ {
		am := map[string]any{}
		for bb := 0; bb < 10; bb++ {
			bm := map[string]any{}
			for cc := 0; cc < 10; cc++ {
				cm := map[string]any{}
				for d := 0; d < 10; d++ {
					cm[fmt.Sprintf("d%d", d)] = a*1000 + bb*100 + cc*10 + d
				}
				bm[fmt.Sprintf("c%d", cc)] = cm
			}
			am[fmt.Sprintf("b%d", bb)] = bm
		}
		big[fmt.Sprintf("a%d", a)] = am
	}
	c.s.data = map[string]any{"big": big}
	var cfg benchCfg
	if err := c.BindRef("app", &cfg); err != nil {
		b.Fatal(err)
	}
	return c
}

var sink any
var sinkB bool

func BenchmarkGetLeaf(b *testing.B) {
	c := newBenchConic(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink = c.Get("big.a3.b4.c5.d6")
	}
}

func BenchmarkGetSubtree(b *testing.B) {
	c := newBenchConic(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink = c.Get("big.a3.b4")
	}
}

func BenchmarkIsSet(b *testing.B) {
	c := newBenchConic(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkB = c.IsSet("big.a3.b4.c5.d6")
	}
}

func BenchmarkGetLeafParallel(b *testing.B) {
	c := newBenchConic(b)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		var v any
		for pb.Next() {
			v = c.Get("big.a3.b4.c5.d6")
		}
		_ = v
	})
}
