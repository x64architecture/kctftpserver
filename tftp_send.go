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

func sendTftpAckPkt(client *ClientState, blockNum uint16) (int, error) {
	pkt := buildTftpAckPkt(blockNum)
	n, err := client.conn.WriteToUDP(*pkt, &client.addr)
	return n, err
}

func sendTftpOackPkt(client *ClientState, options *[]TftpOption) (int, error) {
	pkt := buildTftpOackPkt(options)
	n, err := client.conn.WriteToUDP(*pkt, &client.addr)
	return n, err
}

func sendTftpDataPkt(client *ClientState, blockNum uint16, data []byte) (int, error) {
	pkt := buildTftpDataPkt(blockNum, data)
	n, err := client.conn.WriteToUDP(*pkt, &client.addr)
	return n, err
}

func sendTftpErrorPkt(client *ClientState, errorCode uint16, errorMsg string) (int, error) {
	pkt := buildTftpErrorPkt(errorCode, errorMsg)
	n, err := client.conn.WriteToUDP(*pkt, &client.addr)
	return n, err
}
