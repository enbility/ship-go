# ship-go

[![Build Status](https://github.com/enbility/ship-go/actions/workflows/default.yml/badge.svg?branch=dev)](https://github.com/enbility/ship-go/actions/workflows/default.yml/badge.svg?branch=dev)
[![GoDoc](https://img.shields.io/badge/godoc-reference-5272B4)](https://godoc.org/github.com/enbility/ship-go)
[![Coverage Status](https://coveralls.io/repos/github/enbility/ship-go/badge.svg?branch=dev)](https://coveralls.io/github/enbility/ship-go?branch=dev)
[![Go report](https://goreportcard.com/badge/github.com/enbility/ship-go)](https://goreportcard.com/report/github.com/enbility/ship-go)
[![CodeFactor](https://www.codefactor.io/repository/github/enbility/ship-go/badge)](https://www.codefactor.io/repository/github/enbility/ship-go)

This library provides an implementation of SHIP 1.0.1 in [go](https://golang.org), which is part of the [EEBUS](https://eebus.org) specification.

Basic understanding of the EEBUS concepts SHIP and SPINE to use this library is required. Please check the corresponding specifications on the [EEBUS downloads website](https://www.eebus.org/media-downloads/).

This repository was started as part of the [eebus-go](https://github.com/enbility/eebus-go) before it was moved into its own repository and this separate go package.

## Overview

Includes:

- Certificate handling
- mDNS, incl. avahi support (recommended)
- Websocket server and client
- Connection handling, including reconnection and double connections
- Handling of device pairing
- SHIP handshake
- Logging which is also used by [spine-go](https://github.com/enbility/spine-go) and [eebus-go](https://github.com/enbility/eebus-go)

## Implementation notes

- Double connection handling is not implemented according to SHIP 12.2.2. Instead the connection initiated by the higher SKI will be kept. Much simpler and always works
- PIN Verification is _NOT_ supported other than SHIP 13.4.4.3.5.1 _"none"_ PIN state is supported!
- Access Methods SHIP 13.4.6 only supports the most basic scenario and only works after PIN verification state is completed.
- Supported registration mechanisms (SHIP 5):
  - auto accept (without any interaction mechanism!)
  - user verification
