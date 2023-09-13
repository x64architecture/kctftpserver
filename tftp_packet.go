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
	"encoding/binary"
	"fmt"
)

const (
	OPCODE_RRQ   = uint16(1) // Read request (RRQ)
	OPCODE_WRQ   = uint16(2) // Write request (WRQ)
	OPCODE_DATA  = uint16(3) // Data (DATA)
	OPCODE_ACK   = uint16(4) // Acknowledgment (ACK)
	OPCODE_ERROR = uint16(5) // Error (ERROR)
	OPCODE_OACK  = uint16(6) // Option Acknowledgment (OACK)
)

const (
	ERROR_UNDEFINED       = uint16(0) // Not defined, see error message (if any).
	ERROR_FILENOTFOUND    = uint16(1) // File not found.
	ERROR_ACCESSVIOLATION = uint16(2) // Access violation.
	ERROR_DISKFULL        = uint16(3) // Disk full or allocation exceeded.
	ERROR_ILLEGAL         = uint16(4) // Illegal TFTP operation.
	ERROR_UNKNOWNTID      = uint16(5) // Unknown transfer ID.
	ERROR_FILEEXISTS      = uint16(6) // File already exists.
	ERROR_NXUSER          = uint16(7) // No such user.
	ERROR_OACK            = uint16(8) // RFC2347 option negotiation failed.
)

type TftpOption struct {
	name  string
	value string
}

type RQPkt struct {
	fileName string
	mode     string
	options  []TftpOption
}

type DataPkt struct {
	blockNum uint16
	data     []byte
}

func getStrFromPkt(pkt *[]byte, pktLen int, index int) []byte {
	for i := index; i < pktLen; i++ {
		if (*pkt)[i] == 0 {
			return (*pkt)[index:i]
		}
	}
	return nil
}

func getOpcodeFromPkt(pkt *[]byte) uint16 {
	return binary.BigEndian.Uint16(*pkt)
}

func getBlkNumFromAckPkt(pkt *[]byte) (uint16, error) {
	blockNum := binary.BigEndian.Uint16((*pkt)[2:])
	return blockNum, nil
}

func deserializeDataPkt(pkt *[]byte, pktLen int) (*DataPkt, error) {
	if pktLen < 4 {
		return &DataPkt{}, fmt.Errorf("invalid packet len")
	}
	n := 2 /* Skip 2-byte Opcode */
	blockNum := binary.BigEndian.Uint16((*pkt)[n:])
	n += 2 /* Skip 2-byte blockNum */

	/* Check for empty packet */
	var data []byte
	if pktLen == n {
		data = []byte{}
	} else {
		data = (*pkt)[n:pktLen]
	}

	return &DataPkt{blockNum, data}, nil
}

func deserializeRQPkt(pkt *[]byte, pktLen int) (*RQPkt, error) {
	if pktLen < 6 {
		return &RQPkt{}, fmt.Errorf("invalid packet len")
	}
	n := 2 /* Skip 2-byte Opcode */
	fileName := getStrFromPkt(pkt, pktLen, n)
	if fileName == nil {
		return &RQPkt{}, fmt.Errorf("failed to read 'Filename' field")
	}
	n += len(fileName)
	n += 1 /* Skip NUL byte */
	mode := getStrFromPkt(pkt, pktLen, n)
	if mode == nil {
		return &RQPkt{}, fmt.Errorf("failed to read 'mode' field")
	}
	n += len(mode)
	n += 1 /* Skip NUL byte */

	if n >= pktLen {
		return &RQPkt{string(fileName), string(mode), nil}, nil
	}

	/* RFC 2347 */
	var options = make([]TftpOption, 0, 1)
	for n < pktLen {
		optionName := getStrFromPkt(pkt, pktLen, n)
		if optionName == nil {
			return &RQPkt{}, fmt.Errorf("failed to read 'name' field")
		}
		n += len(optionName)
		n += 1 /* Skip NUL byte */
		optionValue := getStrFromPkt(pkt, pktLen, n)
		if optionValue == nil {
			return &RQPkt{}, fmt.Errorf("failed to read 'value' field")
		}
		n += len(optionValue)
		n += 1 /* Skip NUL byte */

		options = append(options, TftpOption{string(optionName), string(optionValue)})
	}

	return &RQPkt{string(fileName), string(mode), options}, nil
}

func buildTftpAckPkt(blockNum uint16) *[]byte {
	dataPkt := make([]byte, 0, 4)
	dataPkt = binary.BigEndian.AppendUint16(dataPkt, OPCODE_ACK)
	dataPkt = binary.BigEndian.AppendUint16(dataPkt, blockNum)
	return &dataPkt
}

func buildTftpOackPkt(options *[]TftpOption) *[]byte {
	dataPkt := make([]byte, 0, 4)
	dataPkt = binary.BigEndian.AppendUint16(dataPkt, OPCODE_OACK)
	for _, option := range *options {
		dataPkt = append(dataPkt, []byte(option.name)...)
		dataPkt = append(dataPkt, 0)
		dataPkt = append(dataPkt, []byte(option.value)...)
		dataPkt = append(dataPkt, 0)
	}
	return &dataPkt
}

func buildTftpDataPkt(blockNum uint16, data []byte) *[]byte {
	dataPkt := make([]byte, 0, 4)
	dataPkt = binary.BigEndian.AppendUint16(dataPkt, OPCODE_DATA)
	dataPkt = binary.BigEndian.AppendUint16(dataPkt, blockNum)
	dataPkt = append(dataPkt, data...)
	return &dataPkt
}

func errorCodeToByteArray(errorCode uint16) []byte {
	switch errorCode {
	case ERROR_UNDEFINED:
		return []byte("Undefined error.")
	case ERROR_FILENOTFOUND:
		return []byte("File not found.")
	case ERROR_ACCESSVIOLATION:
		return []byte("Access violation.")
	case ERROR_DISKFULL:
		return []byte("Disk full or allocation exceeded.")
	case ERROR_ILLEGAL:
		return []byte("Illegal TFTP operation.")
	case ERROR_UNKNOWNTID:
		return []byte("Unknown transfer ID.")
	case ERROR_FILEEXISTS:
		return []byte("File already exists.")
	case ERROR_NXUSER:
		return []byte("No such user.")
	case ERROR_OACK:
		return []byte("RFC2347 option negotiation failed.")
	default:
		return nil
	}
}

func buildTftpErrorPkt(errorCode uint16, errorMsg string) *[]byte {
	dataPkt := make([]byte, 0, 4+len(errorMsg)+1)
	dataPkt = binary.BigEndian.AppendUint16(dataPkt, OPCODE_ERROR)
	dataPkt = binary.BigEndian.AppendUint16(dataPkt, errorCode)
	if len(errorMsg) != 0 {
		dataPkt = append(dataPkt, []byte(errorMsg)...)
	} else {
		dataPkt = append(dataPkt, errorCodeToByteArray(errorCode)...)
	}
	dataPkt = append(dataPkt, 0)
	return &dataPkt
}
