/*
 * Copyright (c) 2023, Kurt Cancemi (kurt@x64architecture.com)
 *
 * This file is part of KC TFTP Server.
 *
 *  KC TFTP Server is free software: you can redistribute it and/or modify
 *  it under the terms of the GNU General Public License version 3 as
 *  published by the Free Software Foundation.
 *
 *  KC TFTP Server is distributed in the hope that it will be useful,
 *  but WITHOUT ANY WARRANTY; without even the implied warranty of
 *  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 *  GNU General Public License for more details.
 *
 *  You should have received a copy of the GNU General Public License
 *  along with KC TFTP Server. If not, see <http://www.gnu.org/licenses/>.
 */
package main

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

type (
	KcTftpServerConfig struct {
		hostPort   string // host:port to listen on
		supportGet bool   // Support RRQ requests
		supportPut bool   // Support WRQ requests
		getDir     string // Directory to serve RRQ requests
		putDir     string // Directory to serve WRQ requests
		maxBlkSize uint16 // Maximum block size
		timeout    uint16 // Maximum time to wait for a client to respond in seconds
	}

	ClientState struct {
		serverConfig *KcTftpServerConfig
		conn         *net.UDPConn
		addr         net.UDPAddr
		options      []TftpOption
		blockSize    uint16
		windowSize   uint16
		acks         []uint16
		dataPkts     []DataPkt
		mutex        sync.Mutex
	}
)

var (
	globalClients map[netip.AddrPort]*ClientState
)

func processOptions(client *ClientState, sentOptions *[]TftpOption, fileSize int64) error {
	for _, option := range *sentOptions {
		switch strings.ToLower(option.name) {
		case "blksize":
			value, err := strconv.ParseUint(option.value, 10, 16)
			if err != nil {
				log.Error().Err(err).Msgf("Bad blksize (%s) recieved from %s", option.value, client.addr.String())
				value = 512
			}
			valueU16 := uint16(value)
			if valueU16 > client.serverConfig.maxBlkSize {
				valueU16 = client.serverConfig.maxBlkSize
			}
			client.blockSize = valueU16
			client.options = append(client.options, TftpOption{option.name, strconv.FormatUint(uint64(client.blockSize), 10)})
		case "windowsize":
			value, err := strconv.ParseUint(option.value, 10, 16)
			if err != nil {
				log.Error().Err(err).Msgf("Bad windowsize (%s) recieved from %s", option.value, client.addr.String())
				value = 1
			}
			client.windowSize = uint16(value)
			client.options = append(client.options, TftpOption{option.name, strconv.FormatUint(uint64(client.windowSize), 10)})
		case "tsize":
			if fileSize == -1 {
				continue
			}
			client.options = append(client.options, TftpOption{option.name, strconv.FormatInt(fileSize, 10)})
		}
	}
	log.Debug().Msgf("Sending OACK %s to %s", client.options, client.addr.String())
	_, err := sendTftpOackPkt(client, &client.options)
	if err != nil {
		return err
	}

	return nil
}

func waitForAck(client *ClientState) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(client.serverConfig.timeout)*time.Second)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("time out waiting for ACK")
		default:
			client.mutex.Lock()
			acksAvailiable := len(client.acks)
			client.mutex.Unlock()
			if acksAvailiable > 0 {
				return nil
			}
		}
	}
}

func waitForData(client *ClientState) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(client.serverConfig.timeout)*time.Second)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("time out waiting for DATA")
		default:
			client.mutex.Lock()
			dataPktsAvailiable := len(client.dataPkts)
			client.mutex.Unlock()
			if dataPktsAvailiable > 0 {
				return nil
			}
		}
	}
}

func handleRRQ(client *ClientState, buffer *[]byte, pktSize int) error {
	rqPkt, err := deserializeRQPkt(buffer, pktSize)
	if err != nil {
		return err
	}

	log.Info().Msgf("Recieved RRQ: filename: '%s' mode: '%s' options: '%s' from %s", rqPkt.fileName, rqPkt.mode, rqPkt.options, client.addr.String())
	fileNameSanitized := filepath.Join(client.serverConfig.getDir, filepath.Clean(filepath.Join("/", rqPkt.fileName)))
	log.Debug().Msgf("fileNameSanitized: '%s'", fileNameSanitized)

	f, err := os.Open(fileNameSanitized)
	if err != nil {
		if os.IsNotExist(err) {
			sendTftpErrorPkt(client, ERROR_FILENOTFOUND, "")
		} else if os.IsPermission(err) {
			sendTftpErrorPkt(client, ERROR_ACCESSVIOLATION, "")
		} else {
			sendTftpErrorPkt(client, ERROR_UNDEFINED, "")
		}
		return err
	}

	info, err := f.Stat()
	if err != nil {
		sendTftpErrorPkt(client, ERROR_UNDEFINED, err.Error())
		return err
	}

	if len(rqPkt.options) > 0 {
		err := processOptions(client, &rqPkt.options, info.Size())
		if err != nil {
			sendTftpErrorPkt(client, ERROR_OACK, "")
			return err
		}
		log.Debug().Msgf("len(client.acks): '%d'", len(client.acks))
		err = waitForAck(client)
		if err != nil {
			sendTftpErrorPkt(client, ERROR_UNDEFINED, err.Error())
			return err
		}
		client.mutex.Lock()
		ack := client.acks[0]
		client.acks = client.acks[1:]
		client.mutex.Unlock()
		if ack != 0 {
			log.Error().Msg("Invalid OACK sent")
			sendTftpErrorPkt(client, ERROR_OACK, "")
			return nil
		}
	}

	blockNum := uint16(1)
	remainingBytes := info.Size()
	if (remainingBytes / int64(client.blockSize)) > 65535 {
		log.Error().Msgf("File would not fit within (2^16)-1 blocks %d", remainingBytes/int64(client.blockSize))
		sendTftpErrorPkt(client, ERROR_ILLEGAL, "File would not fit within (2^16)-1 blocks")
		return nil
	}

	for remainingBlocks := remainingBytes / int64(client.blockSize); remainingBlocks > 0; remainingBlocks-- {
		data := make([]byte, client.blockSize)
		_, err := f.Read(data)
		if err != nil {
			sendTftpErrorPkt(client, ERROR_UNDEFINED, "Error reading file.")
			return err
		}
		log.Debug().Msgf("Sending DATA (block=%d,len=%d) to %s", blockNum, client.blockSize, client.addr.String())
		sendTftpDataPkt(client, blockNum, data)

		// RFC 7440

		if blockNum%client.windowSize == 0 {
			log.Debug().Msgf("len(client.acks): '%d'", len(client.acks))
			err = waitForAck(client)
			if err != nil {
				sendTftpErrorPkt(client, ERROR_UNDEFINED, err.Error())
				return err
			}
			client.mutex.Lock()
			//ack := client.acks[0] TBD retries
			client.acks = client.acks[1:]
			client.mutex.Unlock()
		}

		blockNum++
	}

	remainingBytes %= int64(client.blockSize)
	var data []byte
	if remainingBytes != 0 {
		data = make([]byte, remainingBytes)
		_, err := f.Read(data)
		if err != nil {
			sendTftpErrorPkt(client, ERROR_UNDEFINED, "Error reading file.")
			return err
		}
	} else {
		data = []byte{}
	}
	log.Debug().Msgf("Sending DATA (block=%d,len=%d) to %s", blockNum, len(data), client.addr.String())
	sendTftpDataPkt(client, blockNum, data)

	log.Debug().Msgf("len(client.acks): '%d'", len(client.acks))
	err = waitForAck(client)
	if err != nil {
		sendTftpErrorPkt(client, ERROR_UNDEFINED, err.Error())
		return err
	}
	//ack := client.acks[0] TBD retries
	client.mutex.Lock()
	client.acks = client.acks[1:]
	client.mutex.Unlock()

	return nil
}

func handleWRQ(client *ClientState, buffer *[]byte, pktSize int) error {
	rqPkt, err := deserializeRQPkt(buffer, pktSize)
	if err != nil {
		sendTftpErrorPkt(client, ERROR_UNDEFINED, "")
		return err
	}

	log.Info().Msgf("Recieved WRQ: filename: '%s' mode: '%s' options: '%s' from %s", rqPkt.fileName, rqPkt.mode, rqPkt.options, client.addr.String())
	fileNameSanitized := filepath.Join(client.serverConfig.putDir, filepath.Clean("/"+rqPkt.fileName))
	log.Debug().Msgf("fileNameSanitized: '%s'", fileNameSanitized)

	blockNum := uint16(0)
	if len(rqPkt.options) > 0 {
		err := processOptions(client, &rqPkt.options, -1)
		if err != nil {
			sendTftpErrorPkt(client, ERROR_OACK, "")
			return err
		}
		client.mutex.Lock()
		ack := client.acks[0]
		client.acks = client.acks[1:]
		client.mutex.Unlock()
		if ack != blockNum {
			log.Error().Msg("Invalid OACK sent")
			sendTftpErrorPkt(client, ERROR_OACK, "")
			return nil
		}
	} else {
		_, err := sendTftpAckPkt(client, blockNum)
		if err != nil {
			sendTftpErrorPkt(client, ERROR_UNDEFINED, "")
			return err
		}
	}
	blockNum++

	if _, err := os.Stat(fileNameSanitized); err == nil {
		log.Error().Msgf("File with name (%s) from client already exists", fileNameSanitized)
		sendTftpErrorPkt(client, ERROR_FILEEXISTS, "")
		return err
	}

	f, _ := os.Create(fileNameSanitized)
	if err != nil {
		if os.IsPermission(err) {
			sendTftpErrorPkt(client, ERROR_ACCESSVIOLATION, "")
		} else {
			sendTftpErrorPkt(client, ERROR_UNDEFINED, "")
		}
		return err
	}
	defer f.Close()

	wrqDoneblockNum := uint16(0)
	for {
		err := waitForData(client)
		if err != nil {
			sendTftpErrorPkt(client, ERROR_UNDEFINED, err.Error())
			return err
		}
		client.mutex.Lock()
		dataPkt := client.dataPkts[0]
		client.dataPkts = client.dataPkts[1:]
		client.mutex.Unlock()

		if dataLen := len(dataPkt.data); dataLen != 0 {
			_, err := f.WriteAt(dataPkt.data, int64(dataLen)*int64(dataPkt.blockNum-1))
			if err != nil {
				sendTftpErrorPkt(client, ERROR_UNDEFINED, "")
				return err
			}
		}
		if len(dataPkt.data) != int(client.blockSize) {
			wrqDoneblockNum = dataPkt.blockNum
		}
		if blockNum == wrqDoneblockNum {
			log.Debug().Msgf("Sending ACK (block=%d) to %s", wrqDoneblockNum, client.addr.String())
			sendTftpAckPkt(client, wrqDoneblockNum)
			log.Debug().Msg("handleWRQ(): wrqDone")
			break
		} else if dataPkt.blockNum%client.windowSize == 0 && dataPkt.blockNum != wrqDoneblockNum {
			log.Debug().Msgf("Sending ACK (block=%d) to %s", dataPkt.blockNum, client.addr.String())
			sendTftpAckPkt(client, dataPkt.blockNum)
		}
		blockNum++
	}

	return nil
}

func handleData(client *ClientState, buffer *[]byte, pktSize int) error {
	dataPkt, err := deserializeDataPkt(buffer, pktSize)
	if err != nil {
		return err
	}
	log.Debug().Msgf("Recieved DATA (block=%d, dataSize=%d) from %s", dataPkt.blockNum, len(dataPkt.data), client.addr.String())
	client.mutex.Lock()
	client.dataPkts = append(client.dataPkts, *dataPkt)
	client.mutex.Unlock()

	return nil
}

func handleACK(client *ClientState, buffer *[]byte, pktSize int) error {
	if pktSize < 4 {
		return fmt.Errorf("invalid packet len")
	}
	blockNum, err := getBlkNumFromAckPkt(buffer)
	if err != nil {
		sendTftpErrorPkt(client, ERROR_UNDEFINED, err.Error())
		return err
	}
	log.Debug().Msgf("Recieved ACK (block=%d) from %s", blockNum, client.addr.String())
	client.mutex.Lock()
	client.acks = append(client.acks, blockNum)
	client.mutex.Unlock()

	return nil
}

func serveRRQ(client *ClientState, buffer *[]byte, pktSize int) {
	err := handleRRQ(client, buffer, pktSize)
	if err != nil {
		log.Error().Err(err).Msg(client.addr.String())
	}
	delete(globalClients, client.addr.AddrPort())
}

func serveWRQ(client *ClientState, buffer *[]byte, pktSize int) {
	err := handleWRQ(client, buffer, pktSize)
	if err != nil {
		log.Error().Err(err).Msg(client.addr.String())
	}
	delete(globalClients, client.addr.AddrPort())
}

func serveData(client *ClientState, buffer *[]byte, pktSize int) {
	err := handleData(client, buffer, pktSize)
	if err != nil {
		delete(globalClients, client.addr.AddrPort())
		log.Error().Err(err).Msg(client.addr.String())
	}
}

func serveACK(client *ClientState, buffer *[]byte, pktSize int) {
	err := handleACK(client, buffer, pktSize)
	if err != nil {
		delete(globalClients, client.addr.AddrPort())
		log.Error().Err(err).Msg(client.addr.String())
	}
}

func serveUnknownTID(client *ClientState, opcode uint16) {
	log.Info().Msgf("Unknown client/TID (%s) for opcode (%d) ", client.addr.String(), opcode)
	sendTftpErrorPkt(client, ERROR_UNKNOWNTID, "")
}

func serveError(client *ClientState, opcode uint16) {
	log.Info().Msgf("Illegal or unexpected opcode (%d) from %s", opcode, client.addr.String())
	sendTftpErrorPkt(client, ERROR_ILLEGAL, "")
}

func serveOpUnsupported(client *ClientState, opcode uint16) {
	log.Info().Msgf("Recieved opcode (%d) which is disabled by configuration from %s", opcode, client.addr.String())
	sendTftpErrorPkt(client, ERROR_UNDEFINED, "Operation disabled by configuration on this server.")
}

func initClient(c *KcTftpServerConfig, conn *net.UDPConn, addr *net.UDPAddr) *ClientState {
	var client ClientState
	client.addr = *addr
	client.serverConfig = c
	client.blockSize = 512
	client.windowSize = 1
	client.conn = conn

	return &client
}

func printLocalAddresses(port string) {
	ifaces, err := net.Interfaces()
	if err != nil {
		log.Error().Err(err).Msg("Error printing local addresses")
		return
	}
	for _, intf := range ifaces {
		addrs, err := intf.Addrs()
		if err != nil {
			log.Error().Err(err).Msg("Error printing local addresses")
			continue
		}
		for _, addr := range addrs {
			switch addr := addr.(type) {
			case *net.IPNet:
				log.Info().Msgf("Listening on %s:%s (%s)...", addr.IP.String(), port, intf.Name)
			}
		}
	}
}

func Serve(c *KcTftpServerConfig, wg *sync.WaitGroup) error {
	defer wg.Done()
	serveAddr, err := net.ResolveUDPAddr("udp", c.hostPort)
	if err != nil {
		log.Error().Err(err).Msg("Error resolving interface address")
		return err
	}

	conn, err := net.ListenUDP("udp", serveAddr)
	if err != nil {
		log.Error().Err(err).Msg("")
		return err
	}
	defer conn.Close()

	log.Info().Msgf("TFTP GET Directory (%s)", c.getDir)
	log.Info().Msgf("TFTP PUT Directory (%s)", c.putDir)

	if serveAddr.IP == nil {
		printLocalAddresses(c.hostPort[strings.LastIndex(c.hostPort, ":")+1:])
	} else {
		log.Info().Msgf("Listening on %s...", serveAddr.IP.String())
	}

	globalClients = make(map[netip.AddrPort]*ClientState, 16)
	udpReadBufSize := 1024 + c.maxBlkSize

	for {
		buffer := make([]byte, udpReadBufSize)
		bytesRead, addr, err := conn.ReadFromUDP(buffer)
		if err != nil {
			log.Error().Err(err).Msg(addr.String())
			continue
		}
		if bytesRead < 2 {
			continue
		}
		var client *ClientState = nil
		var trackedClient bool
		if globalClients[addr.AddrPort()] != nil {
			client = globalClients[addr.AddrPort()]
			trackedClient = true
		} else {
			client = initClient(c, conn, addr)
			trackedClient = false
		}

		opcode := getOpcodeFromPkt(&buffer)
		switch opcode {
		case OPCODE_RRQ:
			if !c.supportGet {
				go serveOpUnsupported(client, opcode)
				continue
			}
			globalClients[addr.AddrPort()] = client
			go serveRRQ(client, &buffer, bytesRead)
		case OPCODE_WRQ:
			if !c.supportPut {
				go serveOpUnsupported(client, opcode)
				continue
			}
			globalClients[addr.AddrPort()] = client
			go serveWRQ(client, &buffer, bytesRead)
		case OPCODE_DATA:
			if trackedClient {
				go serveData(client, &buffer, bytesRead)
			} else {
				go serveUnknownTID(client, opcode)
			}
		case OPCODE_ACK:
			if trackedClient {
				go serveACK(client, &buffer, bytesRead)
			} else {
				go serveUnknownTID(client, opcode)
			}
		default:
			go serveError(client, opcode)
		}
	}
}
