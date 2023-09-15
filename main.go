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
	"flag"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type (
	KcTftpServerConfigToml struct {
		Verbose bool
		Servers map[string]ServerTable
	}
	ServerTable struct {
		Host           string
		Get_dir        string
		Put_dir        string
		Max_block_size uint16
		Timeout        uint16
		Support_get    bool
		Support_put    bool
	}
)

var (
	gitShortHash = "<GIT SHORT HASH UNDEFINED>"
	version      = "<VERSION UNDEFINED>"
	configFile   string
)

func getConfigFilePath() string {
	if len(configFile) != 0 {
		return configFile
	}

	if exe, err := os.Executable(); err == nil {
		configFile = filepath.Join(filepath.Dir(exe), "/kc_tftp_server.toml")
		if _, err := os.Stat(configFile); err != nil {
			if wd, err := os.Getwd(); err == nil {
				configFile = filepath.Join(wd, "/kc_tftp_server.toml")
			}
		}
	}

	return configFile
}

func processServerTable(md *toml.MetaData, serverName string, s *ServerTable) *KcTftpServerConfig {
	host := s.Host
	getDir := s.Get_dir
	putDir := s.Put_dir
	supportGet := s.Support_get
	supportPut := s.Support_put
	maxBlockSize := s.Max_block_size
	timeOut := s.Timeout

	if !md.IsDefined("servers", serverName, "host") {
		log.Error().Msg("Server [%s] 'host' not specified")
		return nil
	}
	if !md.IsDefined("servers", serverName, "support_get") {
		supportGet = true
	}
	if !md.IsDefined("servers", serverName, "max_block_size") {
		maxBlockSize = 512
	}
	if !md.IsDefined("servers", serverName, "timeout") {
		timeOut = 5
	}

	if supportGet {
		if len(getDir) == 0 {
			log.Error().Msgf("Server [%s] please set the 'get_dir' in the config file", serverName)
			return nil
		}
	}
	getDir, _ = filepath.Abs(getDir)

	if supportPut {
		if len(putDir) == 0 {
			log.Error().Msgf("Server [%s] please set the 'put_dir' in the config file", serverName)
			return nil
		}
	}
	putDir, _ = filepath.Abs(putDir)

	if maxBlockSize < 1 || maxBlockSize > 65535 {
		log.Error().Msgf("Server [%s] Invalid 'max_block_size' specified, 'max_block_size' must be in the range [1,65535]", serverName)
		return nil
	}

	if timeOut < 1 || timeOut > 65535 {
		log.Error().Msgf("Server [%s] Invalid 'timeout' specified, 'timeout' must be in the range [1,65535]", serverName)
		return nil
	}

	return &KcTftpServerConfig{
		host,
		supportGet,
		supportPut,
		getDir,
		putDir,
		maxBlockSize,
		timeOut}
}

func main() {
	output := zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.DateTime}
	log.Logger = log.Output(output)
	log.Info().Msgf("KC TFTP Server %s (%s)", version, gitShortHash)
	log.Info().Msg("Copyright (c) 2023 Kurt Cancemi (kurt <at> x64architecture.com)")
	log.Info().Msg("Licensed under the GNU General Public License version 3 (only)")

	var verbose bool
	var showVersion bool

	flag.BoolVar(&verbose, "verbose", false, "Output verbose information.")
	flag.BoolVar(&showVersion, "version", false, "Output version information.")
	flag.StringVar(&configFile, "configFile", "", "Configuration file location.")
	flag.Parse()

	if showVersion {
		return
	}

	configFilePath := getConfigFilePath()
	log.Info().Msgf("Using config file (%s)", configFilePath)

	var tomlConfig KcTftpServerConfigToml
	md, err := toml.DecodeFile(configFilePath, &tomlConfig)
	if err != nil {
		log.Fatal().Err(err).Msg("Error reading config file")
	}
	if tomlConfig.Verbose {
		verbose = true
	}

	if verbose {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	var wg sync.WaitGroup
	for k, v := range tomlConfig.Servers {
		config := processServerTable(&md, k, &v)
		if config == nil {
			return
		}
		log.Debug().Msgf("Server: '%s', Config: '%+v'", k, config)
		wg.Add(1)
		go Serve(config, &wg)
	}
	wg.Wait()
}
