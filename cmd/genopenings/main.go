// genopenings dumps the SPRT rig's balanced paired-opening set as an
// EPD file for cutechess-cli (-openings format=epd order=sequential),
// so pool-gauntlet runs use an identical, committed opening book.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/zellyn/chess6502/internal/asmbuild"
	"github.com/zellyn/chess6502/internal/chesstest"
	"github.com/zellyn/chess6502/internal/refchess"
	"github.com/zellyn/chess6502/internal/sprt"
)

func main() {
	var (
		n   = flag.Int("n", 40, "number of openings")
		out = flag.String("out", "tools/openings-pool.epd", "output EPD path")
	)
	flag.Parse()
	if err := asmbuild.Build("."); err != nil {
		log.Fatal(err)
	}
	bin, err := os.ReadFile(filepath.Join("asm", "engine.bin"))
	if err != nil {
		log.Fatal(err)
	}
	defs, err := chesstest.ParseDefs(filepath.Join("asm", "defs.inc"))
	if err != nil {
		log.Fatal(err)
	}
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	for i, line := range sprt.GenOpenings(bin, defs, *n, 0) {
		ref, err := refchess.ParseFEN(refchess.StartFEN)
		if err != nil {
			log.Fatal(err)
		}
		for _, ms := range line {
			mv, err := refchess.ParseMove(ms)
			if err != nil {
				log.Fatal(err)
			}
			if err := ref.Make(mv); err != nil {
				log.Fatal(err)
			}
		}
		fmt.Fprintf(f, "%s id \"pool-%02d\";\n", epdFrom(ref.FEN()), i)
	}
}

// epdFrom trims a FEN's halfmove/fullmove fields to EPD's four fields.
func epdFrom(fen string) string {
	fields := 0
	for i := 0; i < len(fen); i++ {
		if fen[i] == ' ' {
			fields++
			if fields == 4 {
				return fen[:i]
			}
		}
	}
	return fen
}
