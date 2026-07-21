package mirror

// generate appends all pseudo-legal moves for the side to move into
// e.moves[ply], in EXACTLY the asm generator's order: piece-list slots
// ascending, per-piece directions in table order, sliders emitting
// nearest-square first, pawn pushes before captures (+0x0F before
// +0x11 for white, -0x0F before -0x11 for black), promotions N,B,R,Q,
// and castling after king steps (kingside before queenside). With
// genCaps, quiet moves are dropped (quiescence generation).
func (e *Engine) generate(genCaps bool) []Move {
	p := &e.Pos
	list := e.moves[p.Ply][:0]

	emit := func(from, to, flags byte) {
		if genCaps && flags&(FlEP|FlPromo) == 0 && p.Board[to] == 0 {
			return // quiet: drop
		}
		list = append(list, Move{from, to, flags})
	}
	promoloop := func(from, to byte) {
		for t := byte(Knight); t <= Queen; t++ {
			emit(from, to, t)
		}
	}

	base := int(p.Side) << 1
	for slot := base; slot < base+16; slot++ {
		from := p.PieceSq[slot]
		if from == NoSq {
			continue
		}
		piece := p.Board[from]
		typ := piece & TypeMask

		switch typ {
		case Pawn:
			if piece&ColorMask == 0 {
				// White pawn: moves up (+0x10).
				to := from + 0x10
				if p.Board[to] == 0 {
					if from&0xF0 == 0x60 {
						promoloop(from, to)
					} else if !genCaps {
						emit(from, to, 0)
						if from&0xF0 == 0x10 {
							to2 := to + 0x10
							if p.Board[to2] == 0 {
								emit(from, to2, FlDouble)
							}
						}
					}
				}
				for _, off := range []byte{0x0F, 0x11} {
					to := from + off
					if int(to)&0x88 != 0 {
						continue
					}
					tgt := p.Board[to]
					if tgt == 0 {
						if to == p.EpSq {
							emit(from, to, FlEP)
						}
						continue
					}
					if (tgt^p.Side)&ColorMask == 0 {
						continue // own piece
					}
					if to&0xF0 == 0x70 {
						promoloop(from, to)
					} else {
						emit(from, to, 0)
					}
				}
			} else {
				// Black pawn: moves down (-0x10).
				to := from - 0x10
				if p.Board[to] == 0 {
					if from&0xF0 == 0x10 {
						promoloop(from, to)
					} else if !genCaps {
						emit(from, to, 0)
						if from&0xF0 == 0x60 {
							to2 := to - 0x10
							if p.Board[to2] == 0 {
								emit(from, to2, FlDouble)
							}
						}
					}
				}
				for _, off := range []int{-0x0F, -0x11} {
					to := byte(int(from) + off)
					if int(to)&0x88 != 0 {
						continue
					}
					tgt := p.Board[to]
					if tgt == 0 {
						if to == p.EpSq {
							emit(from, to, FlEP)
						}
						continue
					}
					if (tgt^p.Side)&ColorMask == 0 {
						continue
					}
					if to&0xF0 == 0 {
						promoloop(from, to)
					} else {
						emit(from, to, 0)
					}
				}
			}

		case Knight, King:
			offs := knightOffs
			if typ == King {
				offs = kingOffs
			}
			for _, off := range offs {
				to := int(from) + off
				if to&0x88 != 0 || to < 0 {
					continue
				}
				tgt := p.Board[to]
				if tgt == 0 {
					if !genCaps {
						emit(from, byte(to), 0)
					}
					continue
				}
				if (tgt^p.Side)&ColorMask != 0 {
					emit(from, byte(to), 0)
				}
			}
			if typ == King && !genCaps {
				list = e.genCastle(list)
			}

		default:
			// Sliders: bishop/rook/queen in table direction order.
			var offs []int
			switch typ {
			case Bishop:
				offs = diagOffs
			case Rook:
				offs = orthoOffs
			default:
				offs = kingOffs // queen: all 8, KINGOFF order
			}
			for _, off := range offs {
				to := int(from)
				for {
					to += off
					if to&0x88 != 0 || to < 0 {
						break
					}
					tgt := p.Board[to]
					if tgt == 0 {
						if !genCaps {
							emit(from, byte(to), 0)
						}
						continue
					}
					if (tgt^p.Side)&ColorMask != 0 {
						emit(from, byte(to), 0)
					}
					break
				}
			}
		}
	}
	e.moves[p.Ply] = list
	if e.cyc {
		e.chargeGenerate(len(list))
	}
	return list
}

// genCastle mirrors asm gencastle: rights bit set, between squares
// empty, king square and pass-through square not attacked.
func (e *Engine) genCastle(list []Move) []Move {
	p := &e.Pos
	enemy := p.Side ^ ColorMask
	safe2 := func(a, b byte) bool {
		return !e.attacked(a, enemy) && !e.attacked(b, enemy)
	}
	if p.Side == 0 {
		if p.Castle&CrWK != 0 && p.Board[0x05] == 0 && p.Board[0x06] == 0 &&
			safe2(0x04, 0x05) {
			list = append(list, Move{0x04, 0x06, FlCastle})
		}
		if p.Castle&CrWQ != 0 && p.Board[0x03] == 0 && p.Board[0x02] == 0 &&
			p.Board[0x01] == 0 && safe2(0x04, 0x03) {
			list = append(list, Move{0x04, 0x02, FlCastle})
		}
	} else {
		if p.Castle&CrBK != 0 && p.Board[0x75] == 0 && p.Board[0x76] == 0 &&
			safe2(0x74, 0x75) {
			list = append(list, Move{0x74, 0x76, FlCastle})
		}
		if p.Castle&CrBQ != 0 && p.Board[0x73] == 0 && p.Board[0x72] == 0 &&
			p.Board[0x71] == 0 && safe2(0x74, 0x73) {
			list = append(list, Move{0x74, 0x72, FlCastle})
		}
	}
	return list
}
