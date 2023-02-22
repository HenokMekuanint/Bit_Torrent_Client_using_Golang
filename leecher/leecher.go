package leecher

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"log"
	"os"
	"time"

	"torrent/bitfield"
	"torrent/client"
	"torrent/message"
	"torrent/peers"
)

// MaxBlockSize is the largest number of bytes a request can ask for
const MaxBlockSize = 16384

// MaxRequests is the number of unfulfilled requests a client can queue for
const MaxRequests = 5

// Leecher holds all the data required to download a torrent from a list of peers
type Leecher struct {
	Peers       []peers.Peer
	PeerID      [20]byte
	Port        uint16
	InfoHash    [20]byte
	PieceHashes [][20]byte
	PieceLength int
	Length      int
	Name        string
	File        *os.File
	Bitfield    bitfield.Bitfield
}

type pieceWork struct {
	index  int
	hash   [20]byte
	length int
}

type pieceResult struct {
	index int
	buf   []byte
}

type pieceProgress struct {
	index      int
	client     *client.Client
	buf        []byte
	downloaded int
	requested  int
	backlog    int
}

func (state *pieceProgress) readMessage() error {
	msg, err := state.client.Read() // this call blocks
	if err != nil {
		return err
	}

	if msg == nil { // keep-alive
		return nil
	}

	switch msg.ID {
	case message.MsgUnchoke:
		state.client.Choked = false
	case message.MsgChoke:
		state.client.Choked = true
	case message.MsgHave:
		index, err := message.ParseHave(msg)
		if err != nil {
			return err
		}
		state.client.Bitfield.SetPiece(index)
	case message.MsgPiece:
		n, err := message.ParsePiece(state.index, state.buf, msg)
		if err != nil {
			return err
		}
		state.downloaded += n
		state.backlog--
	}
	return nil
}

func attemptDownloadPiece(c *client.Client, pw *pieceWork) ([]byte, error) {
	state := pieceProgress{
		index:  pw.index,
		client: c,
		buf:    make([]byte, pw.length),
	}
	// Setting a deadline helps get unresponsive peers unstuck.
	// 30 seconds is more than enough time to download a 262 KB piece
	c.Conn.SetDeadline(time.Now().Add(30 * time.Second))
	defer c.Conn.SetDeadline(time.Time{}) // Disable the deadline

	for state.downloaded < pw.length {
		// If unchoked, send requests until we have enough unfulfilled requests
		if !state.client.Choked {
			for state.backlog < MaxRequests && state.requested < pw.length {
				blockSize := MaxBlockSize
				// Last block might be shorter than the typical block
				if pw.length-state.requested < blockSize {
					blockSize = pw.length - state.requested
				}

				err := c.SendRequest(pw.index, state.requested, blockSize)
				if err != nil {
					return nil, err
				}
				state.backlog++
				state.requested += blockSize
			}
		}

		err := state.readMessage()
		if err != nil {
			return nil, err
		}
	}

	return state.buf, nil
}

func checkIntegrity(pw *pieceWork, buf []byte) error {
	hash := sha1.Sum(buf)
	if !bytes.Equal(hash[:], pw.hash[:]) {
		return fmt.Errorf("index %d failed integrity check", pw.index)
	}
	return nil
}

func (t *Leecher) startDownloadWorker(peer peers.Peer, workQueue chan *pieceWork, results chan *pieceResult) {
	c, err := client.New(peer, t.PeerID, t.InfoHash)
	if err != nil {
		log.Printf("Could not handshake with %s. Disconnecting\n", peer.IP)
		return
	}
	defer c.Conn.Close()
	log.Printf("Completed handshake with %s\n", peer.IP)

	c.SendUnchoke()
	c.SendInterested()

	for pw := range workQueue {
		if !c.Bitfield.HasPiece(pw.index) {
			workQueue <- pw // Put piece back on the queue
			continue
		}

		// Download the piece
		buf, err := attemptDownloadPiece(c, pw)
		if err != nil {
			log.Println("Exiting", err)
			workQueue <- pw // Put piece back on the queue
			return
		}

		err = checkIntegrity(pw, buf)
		if err != nil {
			log.Printf("Piece #%d failed integrity check\n", pw.index)
			workQueue <- pw // Put piece back on the queue
			continue
		}

		c.SendHave(pw.index)
		results <- &pieceResult{pw.index, buf}
	}
}

func (t *Leecher) calculateBoundsForPiece(index int) (begin int, end int) {
	begin = index * t.PieceLength
	end = begin + t.PieceLength
	if end > t.Length {
		end = t.Length
	}
	return begin, end
}

func (t *Leecher) calculatePieceSize(index int) int {
	begin, end := t.calculateBoundsForPiece(index)
	return end - begin
}

// Download downloads the torrent. This writes to the file as soon as the piece is downloaded.
func (t *Leecher) Download() error {
	log.Println("Startingload  downfor", t.Name)
	// Init queues for workers to retrieve work and Writes result to file
	donePieces := 0

	workQueue := make(chan *pieceWork, len(t.PieceHashes))
	results := make(chan *pieceResult)
	for index, hash := range t.PieceHashes {
		begin, _ := t.calculateBoundsForPiece(index)
		length := t.calculatePieceSize(index)

		pieceWork := pieceWork{index, hash, length}

		data := make([]byte, length)
		_, err := t.File.ReadAt(data, int64(begin))

		if err != nil {
			fmt.Errorf("something went wrong while trying to Read File", err)
		}

		// Check Integrity of the piece
		integrityerr := checkIntegrity(&pieceWork, data)

		if integrityerr == nil {
			log.Printf("Restored Piece %d from Disk\n", index)
			donePieces += 1
			t.Bitfield.SetPiece(index)
		} else {
			workQueue <- &pieceWork
		}
	}

	// Start workers
	for _, peer := range t.Peers {
		go t.startDownloadWorker(peer, workQueue, results)
	}

	for donePieces < len(t.PieceHashes) {
		res := <-results
		begin, _ := t.calculateBoundsForPiece(res.index)

		// Write to file as soon as it is downloaded
		_, err := t.File.WriteAt(res.buf, int64(begin))
		if err != nil {
			return err
		}
		donePieces++

		percent := float64(donePieces) / float64(len(t.PieceHashes)) * 100
		log.Printf("(%0.2f%%) Downloaded\n", percent)
	}
	log.Printf("Finished Downloading\n")
	close(workQueue)

	return nil
}