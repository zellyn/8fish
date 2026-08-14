// Command sargon boots Sargon III headless in goapple2 and dumps the text
// screen, to prove the harness works and to explore Sargon interactively.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/zellyn/8fish/internal/sargon"
)

// runScript executes a ';'-separated action list against m. Actions:
//
//	wait:N     run N CPU steps
//	type:STR   inject STR as keystrokes (\r for Return)
//	key:HH     inject one key with hex code HH (e.g. 1b for ESC, 01 for ctrl-A, 0d for Return)
//	dump       print the text screen
func runScript(m *sargon.Machine, script string) {
	for _, act := range strings.Split(script, ";") {
		act = strings.TrimSpace(act)
		if act == "" {
			continue
		}
		verb := act
		arg := ""
		if i := strings.IndexByte(act, ':'); i >= 0 {
			verb, arg = act[:i], act[i+1:]
		}
		switch verb {
		case "wait":
			n, err := strconv.ParseUint(arg, 10, 64)
			if err != nil {
				log.Fatalf("bad wait arg %q: %v", arg, err)
			}
			if err := m.Run(n); err != nil {
				log.Fatalf("run: %v", err)
			}
		case "type":
			arg = strings.ReplaceAll(arg, "\\r", "\r")
			m.Type(arg)
		case "key":
			v, err := strconv.ParseUint(arg, 16, 8)
			if err != nil {
				log.Fatalf("bad key arg %q: %v", arg, err)
			}
			m.Key(byte(v))
		case "dump":
			fmt.Printf("--- text screen @ %d steps ---\n", m.Steps)
			fmt.Println(strings.TrimRight(m.Screen(), "\n"))
			fmt.Println("--- end ---")
		case "save":
			ram := m.PeekSlice(0x0000, 0xC000)
			if err := os.WriteFile(arg, ram, 0644); err != nil {
				log.Fatalf("save %q: %v", arg, err)
			}
			fmt.Printf("saved $0000-$BFFF to %s @ %d steps\n", arg, m.Steps)
		default:
			log.Fatalf("unknown action %q", act)
		}
	}
}

// playGame boots Sargon, sets the level, and feeds it the given player moves,
// printing Sargon's reply after each (decoded from RAM, cross-checked with the
// on-screen move text) plus the RAM-derived board.
var easyMode bool
var budgetCycles uint64
var sargonWhite bool
var fenSetup string
var saveRAM string

func playGame(m *sargon.Machine, level int, moves []string) {
	fmt.Println("Booting to move-entry prompt...")
	if err := m.BootToPrompt(); err != nil {
		log.Fatalf("boot: %v", err)
	}
	fmt.Printf("Ready after %d steps. Initial board from RAM:\n%s", m.Steps, m.ReadPieceList().Board())

	if fenSetup != "" {
		if err := m.SetupPosition(fenSetup); err != nil {
			log.Fatalf("setup position: %v", err)
		}
		fmt.Printf("Set up FEN %q. Board from RAM:\n%s", fenSetup, m.ReadPieceList().Board())
		if saveRAM != "" {
			if err := os.WriteFile(saveRAM, m.PeekSlice(0x0000, 0xC000), 0644); err != nil {
				log.Fatalf("save-ram: %v", err)
			}
			fmt.Printf("saved RAM to %s\n", saveRAM)
		}
	}

	if budgetCycles > 0 {
		// Fair-match mechanism: Infinite level + CTRL-T after our cycle budget.
		if err := m.InfiniteLevel(); err != nil {
			log.Fatalf("infinite level: %v", err)
		}
		fmt.Printf("Set Infinite level (SHIFT-9); per-move budget = %d cycles (%.1fs @1.02MHz).\n",
			budgetCycles, float64(budgetCycles)/sargon.CyclesPerSecond)
	} else if level != 1 {
		if err := m.Level(level); err != nil {
			log.Fatalf("level: %v", err)
		}
		fmt.Printf("Set level %d.\n", level)
	}
	if easyMode && budgetCycles == 0 {
		if err := m.EasyMode(); err != nil {
			log.Fatalf("easy mode: %v", err)
		}
		fmt.Println("Enabled Easy Mode (CTRL-E): ponder-free, reliable RAM board reads.")
	}
	if sargonWhite {
		res, err := m.StartAsWhite(budgetCycles)
		if err != nil {
			log.Fatalf("start-as-white: %v", err)
		}
		fmt.Printf("Sargon (White) opens: screen %q  |  RAM %s-%s  |  think %d cyc\nThe -play moves are now BLACK's replies.\n",
			res.SargonText, res.SargonMove.From.Algebraic(), res.SargonMove.To.Algebraic(), res.ThinkCycles)
		fmt.Print(res.Board.Board())
	}

	// Budget scales with level (higher levels think much longer; give margin).
	budget := uint64(15_000_000)
	if level > 2 {
		budget = uint64(level) * 30_000_000
	}
	for i, mv := range moves {
		mv = strings.TrimSpace(mv)
		if mv == "" {
			continue
		}
		fmt.Printf("\n== ply %d: player %s ==\n", i+1, mv)
		var res sargon.MoveResult
		var err error
		if budgetCycles > 0 {
			res, err = m.RequestMove(mv, budgetCycles)
		} else {
			res, err = m.SubmitMove(mv, budget)
		}
		if err != nil {
			fmt.Printf("--- screen on error ---\n%s\n", strings.TrimRight(m.Screen(), "\n"))
			log.Fatalf("submit %q: %v", mv, err)
		}
		ram := res.SargonMove
		fmt.Printf("Sargon replied: screen %q  |  RAM %s-%s (idx %d)  |  think %d cyc (%.2fs @1.02MHz)",
			res.SargonText, ram.From.Algebraic(), ram.To.Algebraic(), ram.MovedIndex,
			res.ThinkCycles, float64(res.ThinkCycles)/sargon.CyclesPerSecond)
		if ram.CapturedIndex >= 0 {
			fmt.Printf("  [captured idx %d]", ram.CapturedIndex)
		}
		if res.Message != "" {
			fmt.Printf("  [%s]", res.Message)
		}
		fmt.Println()
		fmt.Print(res.Board.Board())
		if res.GameOver {
			fmt.Println("game over")
			break
		}
	}
}

func main() {
	dsk := flag.String("dsk", "assets/sargon-iii.dsk", "path to Sargon III .dsk image")
	bootSteps := flag.Uint64("boot", 12_000_000, "CPU steps to run before dumping the screen")
	sample := flag.Uint64("sample", 0, "if >0, dump the screen every N steps up to -boot total")
	script := flag.String("script", "", "after -boot, run a ';'-separated action script (wait:N;type:STR;key:HH;dump)")
	play := flag.String("play", "", "play mode: comma-separated player moves (e.g. E2-E4,D2-D4), driven via the sargon package")
	level := flag.Int("level", 1, "play mode: Sargon level 1-9 (3 = ~30s/move)")
	flag.BoolVar(&easyMode, "easy", true, "play mode: enable Easy Mode (CTRL-E) so Sargon stops pondering (required for reliable RAM board reads)")
	flag.Uint64Var(&budgetCycles, "budget-cycles", 0, "play mode: if >0, use Infinite level + CTRL-T after this many 6502 cycles per move (fair-match mechanism)")
	flag.BoolVar(&sargonWhite, "sargon-white", false, "play mode: Sargon plays White (CTRL-S); -play moves become Black's replies")
	flag.StringVar(&fenSetup, "fen", "", "play mode: set up this FEN/EPD position (poke board) after boot")
	flag.StringVar(&saveRAM, "save-ram", "", "debug: save RAM after fen setup")
	flag.Parse()

	m, err := sargon.NewMachine(*dsk)
	if err != nil {
		log.Fatal(err)
	}

	if *play != "" || fenSetup != "" || sargonWhite || budgetCycles > 0 {
		playGame(m, *level, strings.Split(*play, ","))
		return
	}

	fmt.Printf("Booting %s for %d steps...\n", *dsk, *bootSteps)
	dump := func() {
		fmt.Printf("--- text screen after %d steps ---\n", m.Steps)
		fmt.Println(strings.TrimRight(m.Screen(), "\n"))
		fmt.Println("--- end ---")
	}
	if *sample > 0 {
		for m.Steps < *bootSteps {
			if err := m.Run(*sample); err != nil {
				log.Fatalf("run: %v (after %d steps)", err, m.Steps)
			}
			dump()
		}
		return
	}
	if err := m.Run(*bootSteps); err != nil {
		log.Fatalf("run: %v (after %d steps)", err, m.Steps)
	}
	if *script != "" {
		runScript(m, *script)
		return
	}
	dump()
}
