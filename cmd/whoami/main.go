package main

import (
	"fmt"
	"os"
	"os/user"
)

func main() {
	usr, err := user.Current()
	if err != nil {
		uid := os.Geteuid()

		fmt.Fprintf(os.Stderr, "cannot find name for user ID %d", uid)
		os.Exit(1)
	}

	fmt.Println(usr.Username)
}
