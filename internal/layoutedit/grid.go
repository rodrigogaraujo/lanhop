package layoutedit

const longestSide = 9

func trimmed(rows [][]string) [][]string {
	top, bottom, left, right := -1, -1, -1, -1
	for row, cells := range rows {
		for column, cell := range cells {
			if cell == "" {
				continue
			}
			if top < 0 {
				top = row
			}
			bottom = row
			if left < 0 || column < left {
				left = column
			}
			right = max(right, column)
		}
	}
	if top < 0 {
		return nil
	}
	desk := make([][]string, bottom-top+1)
	for row := range desk {
		desk[row] = make([]string, right-left+1)
		if cells := rows[top+row]; left < len(cells) {
			copy(desk[row], cells[left:])
		}
	}
	return desk
}

func framed(rows [][]string) [][]string {
	desk := trimmed(rows)
	if desk == nil {
		desk = [][]string{{""}}
	}
	above, beside := roomToGrow(len(desk)), roomToGrow(len(desk[0]))
	grid := make([][]string, len(desk)+2*above)
	for row := range grid {
		grid[row] = make([]string, len(desk[0])+2*beside)
	}
	for row, cells := range desk {
		copy(grid[above+row][beside:], cells)
	}
	return grid
}

func roomToGrow(side int) int {
	if side < longestSide {
		return 1
	}
	return 0
}
