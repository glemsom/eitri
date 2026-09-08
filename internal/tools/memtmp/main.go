package main

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/glemsom/eitri/internal/tools"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		for i := 0; i < 6; i++ {
			time.Sleep(1 * time.Second)
			out, _ := exec.Command("bash", "-c", "ps aux | grep -E 'yes|bwrap' | grep -v grep").CombinedOutput()
			fmt.Println("---", string(out))
		}
	}()
	o, err := tools.RealRunner.Run(ctx, tools.RunSpec{Name: "bash", Args: []string{"-c", "yes >/dev/null"}})
	fmt.Println("done", err, len(o.Stdout))
	time.Sleep(4 * time.Second)
}
