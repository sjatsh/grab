package grab

import (
	"fmt"
	"testing"
	"time"
)

// TestGet tests grab.Get
func TestGetBatch(t *testing.T) {
	resp, err := GetBatch(10, []BatchReq{
		{
			Dst: "c:\\download\\10000mb",
			Url: "http://mirror.nl.leaseweb.net/speedtest/10000mb.bin",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := <-resp

	ticker := time.NewTicker(time.Second * 10)
	for {
		select {
		case <-ticker.C:
			r.cancel()
			return
		default:
			time.Sleep(time.Second)
			fmt.Printf("%s: %.1f\n", r.Filename, 100*r.Progress())
		}
	}
}
