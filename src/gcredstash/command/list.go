package command

import (
	"fmt"
	"gcredstash"
	"os"
	"sort"
	"strconv"
	"strings"
)

type ListCommand struct {
	Meta
}

func maxNameLen(items *map[*string]*string) (max_len int) {
	for name, _ := range *items {
		name_len := len(*name)

		if name_len > max_len {
			max_len = name_len
		}
	}

	return
}

func (c *ListCommand) Run(args []string) int {
	items, err := gcredstash.ListSecrets(c.Meta.Table)

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err.Error())
		return 1
	}

	max_len := maxNameLen(&items)
	lines := []string{}

	for name, version := range items {
		ver, err := strconv.Atoi(*version)

		if err != nil {
			panic(err)
		}

		lines = append(lines, fmt.Sprintf("%-*s -- version: %d", max_len, *name, ver))
	}

	sort.Strings(lines)

	for _, line := range lines {
		fmt.Println(line)
	}

	return 0
}

func (c *ListCommand) Synopsis() string {
	return "list credentials and their version"
}

func (c *ListCommand) Help() string {
	helpText := `
usage: gcredstash list
`

	return strings.TrimSpace(helpText)
}
