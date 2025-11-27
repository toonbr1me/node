package singbox

import (
	"bufio"
	"context"
	"io"
	"log"
)

func captureLogs(ctx context.Context, reader io.Reader, sink chan<- string) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		select {
		case <-ctx.Done():
			return
		default:
		}

		if sink != nil {
			select {
			case sink <- line:
			default:
			}
		}
		log.Println(line)
	}
}
