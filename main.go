package main

import (
	"flag"
	"log"

	"github.com/boopathi/esc/embed"
)

func main() {
	conf := &embed.Config{}

	flag.StringVar(&conf.OutputFile, "o", "", "Output file, else stdout.")
	flag.StringVar(&conf.Package, "pkg", "main", "Package.")
	flag.StringVar(&conf.Prefix, "prefix", "", "Prefix to strip from filesnames.")
	flag.StringVar(&conf.Ignore, "ignore", "", "Regexp for files we should ignore (for example \\\\.DS_Store).")
	flag.StringVar(&conf.Include, "include", "", "Regexp for files to include. Only files that match will be included.")
	flag.StringVar(&conf.ModTime, "modtime", "", "Unix timestamp to override as modification time for all files.")
	flag.BoolVar(&conf.Private, "private", false, "If true, do not export autogenerated functions.")
	flag.BoolVar(&conf.NoCompression, "no-compress", false, "If true, do not compress files.")
	flag.Parse()
	conf.Files = flag.Args()

	if err := embed.Run(conf); err != nil {
		log.Fatal(err)
	}
}
