package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"
)

const (
	timeFormat = "Jan\t_2\t15:04"
	dateFormat = "Jan\t_2\t 2006"
)

var config = struct {
	blockSize      uint
	formatLongList bool
}{}

type entryInfo struct {
	path string

	fs.FileInfo

	directRequest bool

	numLinks  int
	numBlocks uint

	uID   int
	gID   int
	user  *user.User
	group *user.Group

	subEntries []entryInfo
}

func newEntryInfo(path string, fsInfo fs.FileInfo) entryInfo {
	return entryInfo{
		path: path,

		FileInfo: fsInfo,

		uID: -1, // Unknown
		gID: -1, // Unknown
	}
}

func (e *entryInfo) SafeName() string {
	name := e.path

	if strings.Contains(name, " ") {
		name = fmt.Sprintf("'%s'", name)
	}

	return name
}

func (e *entryInfo) blocksForSize(blockSize uint) uint {
	factor := blockSize / 512

	if e.IsDir() {
		var total uint

		for _, entry := range e.subEntries {
			total += entry.numBlocks
		}

		return total / factor
	}

	return e.numBlocks / factor
}

func init() {
	flag.UintVar(&config.blockSize, "block-size", 1024, "scale sizes by SIZE before printing them")
	flag.BoolVar(&config.formatLongList, "l", false, "use a long listing format")

	flag.Parse()
}

func main() {
	paths := flag.Args()

	entries := make([]entryInfo, 0, len(paths))
	errs := make([]error, 0)
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			// Collect errors for the end
			errs = append(errs, err)
		}

		if info != nil {
			entry := newEntryInfo(path, info)
			entry.directRequest = true

			entries = append(entries, entry)
		}
	}

	errs = append(errs, fetchEntryInfo(entries, false)...)

	if len(errs) > 0 {
		printErrors(errs...)
	}

	entryWriter := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', 0)

	printEntries(entryWriter, entries, false)

	if err := entryWriter.Flush(); err != nil {
		errs = append(errs, err)
		printErrors(err)
	}

	if len(errs) > 0 {
		os.Exit(2)
	}
}

func fetchEntryInfo(entries []entryInfo, recurse bool) []error {
	errs := make([]error, 0)

	for i := range entries {
		if sysStat, ok := entries[i].Sys().(*syscall.Stat_t); ok {
			entries[i].numLinks = int(sysStat.Nlink)
			entries[i].numBlocks = uint(sysStat.Blocks)
			entries[i].uID = int(sysStat.Uid)
			entries[i].gID = int(sysStat.Gid)
		}

		if entries[i].uID != -1 {
			userInfo, err := user.LookupId(strconv.Itoa(entries[i].uID))
			if err != nil {
				errs = append(errs, err)
			}

			entries[i].user = userInfo
		}

		if entries[i].gID != -1 {
			groupInfo, err := user.LookupGroupId(strconv.Itoa(entries[i].gID))
			if err != nil {
				errs = append(errs, err)
			}

			entries[i].group = groupInfo
		}

		if entries[i].IsDir() {
			dirEntries, err := os.ReadDir(entries[i].path)
			if err != nil {
				errs = append(errs, err)
			}

			entries[i].subEntries = make([]entryInfo, len(dirEntries))
			for j, dirEntry := range dirEntries {
				dirInfo, err := dirEntry.Info()
				if err != nil {
					errs = append(errs, err)
				}

				entries[i].subEntries[j] = newEntryInfo(dirEntry.Name(), dirInfo)
			}

			errs = append(errs, fetchEntryInfo(entries[i].subEntries, recurse)...)
		}
	}

	return nil
}

func printEntries(writer io.Writer, entries []entryInfo, recurse bool) {
	directFileEntries := make([]entryInfo, 0)
	directFileNames := make([]string, 0)
	otherEntries := make([]entryInfo, 0)

	for _, entry := range entries {
		switch {
		case entry.directRequest && !entry.IsDir():
			directFileEntries = append(directFileEntries, entry)
			directFileNames = append(directFileNames, entry.SafeName())
		default:
			otherEntries = append(otherEntries, entry)
		}
	}

	switch {
	case !config.formatLongList && len(directFileNames) > 0:
		sort.Strings(directFileNames)
		fmt.Fprintln(writer, strings.Join(directFileNames, "\t\t\t"))
	case config.formatLongList:
		for _, entry := range directFileEntries {
			printLongEntryInfo(writer, entry)
		}
	}

	for i, entry := range otherEntries {
		if i > 0 {
			fmt.Fprintln(writer)
		}

		// Entry header
		if len(entries) > 1 {
			fmt.Fprintf(writer, "%s:\n", entry.SafeName())
		}
		if config.formatLongList {
			fmt.Fprintf(writer, "total %d\n", entry.blocksForSize(config.blockSize))
		}

		switch {
		case !config.formatLongList:
			subFileNames := make([]string, len(entry.subEntries))
			for j, subEntry := range entry.subEntries {
				subFileNames[j] = subEntry.SafeName()
			}

			if len(subFileNames) > 0 {
				sort.Slice(subFileNames, func(i, j int) bool {
					return strings.ToLower(subFileNames[i]) < strings.ToLower(subFileNames[j])
				})
				fmt.Fprintln(writer, strings.Join(subFileNames, "\t\t\t"))
			}
		case config.formatLongList:
			sort.Slice(entry.subEntries, func(i, j int) bool {
				return strings.ToLower(entry.subEntries[i].path) < strings.ToLower(entry.subEntries[j].path)
			})

			for _, entry := range entry.subEntries {
				printLongEntryInfo(writer, entry)
			}
		}

		if recurse {
			printEntries(writer, entry.subEntries, recurse)
		}
	}
}

func printLongEntryInfo(writer io.Writer, entry entryInfo) {
	line := entry.Mode().String()

	line = fmt.Sprintf("%s\t%d", line, entry.numLinks)

	userName := "Unknown"
	if entry.user != nil {
		userName = entry.user.Username
	}
	line = fmt.Sprintf("%s\t%s", line, userName)

	groupName := "Unknown"
	if entry.group != nil {
		groupName = entry.group.Name
	}
	line = fmt.Sprintf("%s\t%s", line, groupName)

	line = fmt.Sprintf("%s\t%d", line, entry.Size())

	modTime := entry.ModTime()
	modTimeFormat := timeFormat
	if modTime.Year() < time.Now().Year() {
		modTimeFormat = dateFormat
	}
	line = fmt.Sprintf("%s\t%s", line, entry.ModTime().Format(modTimeFormat))

	line = fmt.Sprintf("%s\t%s", line, entry.SafeName())

	fmt.Fprintln(writer, line)
}

func printErrors(errs ...error) {
	for _, err := range errs {
		errMsg := formatError(err)
		fmt.Fprintln(os.Stderr, errMsg)
	}
}

func printErrorsAndExit(errs ...error) {
	printErrors(errs...)
	os.Exit(2)
}

func formatError(err error) string {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return fmt.Sprintf("cannot access '%s': %s", pathErr.Path, pathErr.Err)
	}

	return err.Error()
}
