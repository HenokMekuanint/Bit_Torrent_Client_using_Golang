package main

import (
	"log"
	"os"

	"github.com/tech-yush/bittorent-client/bencode"
)

func main() {
	inPath := os.Args[1]
	outPath := os.Args[2]

	torrentfile, err := bencode.Open(inPath)
	if err != nil {
		log.Fatal(err)
	}

	err = torrentfile.DownloadToFile(outPath)
	if err != nil {
		log.Fatal(err)
	}
}
