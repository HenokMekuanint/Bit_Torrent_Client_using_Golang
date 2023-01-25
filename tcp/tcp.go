package tcp

import (
	"bytes"
	"fmt"
	"net"
	"time"

	"github.com/tech-yush/bittorent-client/bitfield"
	"github.com/tech-yush/bittorent-client/handshake"
	"github.com/tech-yush/bittorent-client/message"
	"github.com/tech-yush/bittorent-client/peers"
)

type TCP struct {
	Conn     net.Conn
	Choked   bool
	Bitfield bitfield.Bitfield
	peer     peers.Peer
	infoHash [20]byte
	peerID   [20]byte
}

func completeHandshake(conn net.Conn, infohash, peerID [20]byte) (*handshake.Handshake, error) {
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	defer conn.SetDeadline(time.Time{})

	req := handshake.Handshake{
		Pstr:     "BitTorrent protocol",
		InfoHash: infohash,
		PeerID:   peerID,
	}
	_, err := conn.Write(req.Serialize())
	if err != nil {
		return nil, err
	}

	res, err := handshake.Read(conn)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(res.InfoHash[:], infohash[:]) {
		return nil, fmt.Errorf("expected infohash %x but got %x", res.InfoHash, infohash)
	}
	return res, nil
}

func recvBitfield(conn net.Conn) (bitfield.Bitfield, error) {
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	defer conn.SetDeadline(time.Time{}) // Disable the deadline

	msg, err := message.Read(conn)
	if err != nil {
		return nil, err
	}
	if msg.ID != message.MsgBitfield {
		err := fmt.Errorf("expected bitfield but got ID %d", msg.ID)
		return nil, err
	}

	return msg.Payload, nil
}

func New(peer peers.Peer, peerID, infoHash [20]byte) (*TCP, error) {
	conn, err := net.DialTimeout("tcp", peer.String(), 3*time.Second)
	if err != nil {
		return nil, err
	}

	_, err = completeHandshake(conn, infoHash, peerID)
	if err != nil {
		conn.Close()
		return nil, err
	}

	bf, err := recvBitfield(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}

	return &TCP{
		Conn:     conn,
		Choked:   true,
		Bitfield: bf,
		peer:     peer,
		infoHash: infoHash,
		peerID:   peerID,
	}, nil
}

func (t *TCP) Read() (*message.Message, error) {
	msg, err := message.Read(t.Conn)
	return msg, err
}

func (t *TCP) SendRequest(index, begin, length int) error {
	req := message.FormatRequest(index, begin, length)
	_, err := t.Conn.Write(req.Serialize())
	return err
}

func (t *TCP) SendInterested() error {
	msg := message.Message{ID: message.MsgInterested}
	_, err := t.Conn.Write(msg.Serialize())
	return err
}

func (t *TCP) SendNotInterested() error {
	msg := message.Message{ID: message.MsgNotInterested}
	_, err := t.Conn.Write(msg.Serialize())
	return err
}

func (t *TCP) SendUnchoke() error {
	msg := message.Message{ID: message.MsgUnchoke}
	_, err := t.Conn.Write(msg.Serialize())
	return err
}

func (t *TCP) SendHave(index int) error {
	msg := message.FormatHave(index)
	_, err := t.Conn.Write(msg.Serialize())
	return err
}
