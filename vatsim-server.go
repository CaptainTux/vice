// vatsim-server.go
// Copyright(c) 2022 Matt Pharr, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package main

import (
	"bufio"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"time"
)

var (
	// All of the servers we can connect to, maintained as a map from
	// server name to hostname or IP address.
	vatsimServers map[string]string = map[string]string{}
)

func vatsimInit() {
	// Disabled so long as we're not official.
	return

	if resp, err := http.Get("http://data.vatsim.net/vatsim-servers.txt"); err != nil {
		ShowErrorDialog("Error retrieving list of VATSIM servers: " + err.Error())
	} else {
		defer resp.Body.Close()

		inServers := false
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if len(line) == 0 || line[0] == ';' {
				continue
			}

			if line == "!SERVERS:" {
				inServers = true
			} else if line[0] == '!' {
				inServers = false
			} else if inServers {
				fields := strings.Split(line, ":")
				vatsimServers[fields[0]+" ("+fields[2]+")"] = fields[1]
				lg.Printf("added %s -> %s", fields[0]+" ("+fields[2]+")", fields[1])
			}
		}
	}
}

type MalformedMessageError struct {
	Err string
}

func (m MalformedMessageError) Error() string {
	return m.Err
}

var (
	// We maintain these as global variables so that they can be
	// initialized by init() functions when we compile with the secret
	// parts required for full VATSIM support.
	vatsimMessageSpecs           []*VATSIMMessageSpec
	vatsimUpdateCallback         func(v *VATSIMServer)
	makeVatsimAircraftController func(v *VATSIMServer) AircraftController = func(v *VATSIMServer) AircraftController {
		return &InertAircraftController{}
	}
)

///////////////////////////////////////////////////////////////////////////
// VATSIMServer

type VATSIMServer struct {
	callsign string

	connection      VATSIMConnection
	controlDelegate AircraftController

	// Various things we need to keep track of over the course of a
	// session.
	aircraft    map[string]*Aircraft
	users       map[string]*User
	controllers map[string]*Controller
	pilots      map[string]*Pilot
	metar       map[string]METAR
	atis        map[string]string

	// Map from callsign to the controller currently tracking the aircraft (if any).
	// Note that we don't require that we have the controller in |controllers|; we
	// may see messages from out-of-range ones...
	trackingControllers map[string]string
	// Ones that we are tracking but have offered to another: callsign->dest. controller
	outboundHandoffs map[string]string
	// Ones that we are tracking but have offered to another: callsign->offering controller
	inboundHandoffs map[string]string

	windowTitle string
	myip        string
	atcValid    bool
}

func NewVATSIMServer() *VATSIMServer {
	return &VATSIMServer{
		aircraft:            make(map[string]*Aircraft),
		controllers:         make(map[string]*Controller),
		metar:               make(map[string]METAR),
		atis:                make(map[string]string),
		users:               make(map[string]*User),
		pilots:              make(map[string]*Pilot),
		trackingControllers: make(map[string]string),
		outboundHandoffs:    make(map[string]string),
		inboundHandoffs:     make(map[string]string),
	}
}

func NewVATSIMNetworkServer(address string) (*VATSIMServer, error) {
	v := NewVATSIMServer()
	v.callsign = positionConfig.VatsimCallsign
	v.windowTitle = ", position: " + positionConfig.VatsimCallsign + " @ " + address

	var err error
	if v.connection, err = NewVATSIMNetConnection(address); err != nil {
		return nil, err
	}
	v.controlDelegate = makeVatsimAircraftController(v)

	loc, _ := database.Locate(positionConfig.PrimaryRadarCenter)

	v.controllers[positionConfig.VatsimCallsign] = &Controller{
		callsign:   positionConfig.VatsimCallsign,
		name:       globalConfig.VatsimName,
		cid:        globalConfig.VatsimCID,
		rating:     globalConfig.VatsimRating,
		scopeRange: int(positionConfig.RadarRange),
		facility:   positionConfig.VatsimFacility,
		location:   loc}

	return v, nil
}

func NewVATSIMReplayServer(filename string, offsetSeconds int, replayRate float32) (*VATSIMServer, error) {
	v := NewVATSIMServer()
	v.callsign = "(none)"
	v.windowTitle = ", replay: " + filename
	v.controlDelegate = &InertAircraftController{}

	var err error
	if v.connection, err = NewVATSIMReplayConnection(filename, offsetSeconds, replayRate); err != nil {
		return nil, err
	} else {
		return v, nil
	}
}

///////////////////////////////////////////////////////////////////////////
// ATCServer method implementations

func (v *VATSIMServer) GetAircraft(callsign string) *Aircraft {
	if ac, ok := v.aircraft[callsign]; ok {
		return ac
	} else {
		return nil
	}
}

func (v *VATSIMServer) GetFilteredAircraft(filter func(*Aircraft) bool) []*Aircraft {
	var filtered []*Aircraft
	for _, ac := range v.aircraft {
		if filter(ac) {
			filtered = append(filtered, ac)
		}
	}
	return filtered
}

func (v *VATSIMServer) GetAllAircraft() []*Aircraft {
	_, ac := FlattenMap(v.aircraft)
	return ac
}

func (v *VATSIMServer) GetMETAR(location string) *METAR {
	if m, ok := v.metar[location]; ok {
		return &m
	} else {
		return nil
	}
}

func (v *VATSIMServer) GetATIS(airport string) string {
	if atis, ok := v.atis[airport]; ok {
		return atis
	} else {
		return ""
	}
}

func (v *VATSIMServer) GetUser(callsign string) *User {
	if user, ok := v.users[callsign]; ok {
		return user
	} else {
		return nil
	}
}

func (v *VATSIMServer) GetController(callsign string) *Controller {
	if controller, ok := v.controllers[callsign]; ok {
		return controller
	} else {
		return nil
	}
}

func (v *VATSIMServer) GetAllControllers() []*Controller {
	_, c := FlattenMap(v.controllers)
	sort.Slice(c, func(i, j int) bool { return c[i].callsign < c[j].callsign })
	return c
}

func (v *VATSIMServer) GetTrackingController(callsign string) string {
	if tc, ok := v.trackingControllers[callsign]; ok {
		return tc
	} else {
		return ""
	}
}

func (v *VATSIMServer) InboundHandoffController(callsign string) string {
	if controller, ok := v.inboundHandoffs[callsign]; ok {
		return controller
	} else {
		return ""
	}
}

func (v *VATSIMServer) OutboundHandoffController(callsign string) string {
	if controller, ok := v.outboundHandoffs[callsign]; ok {
		return controller
	} else {
		return ""
	}
}

func (v *VATSIMServer) SetSquawk(callsign string, code Squawk) error {
	if !v.atcValid {
		return ErrNotController
	} else if ac := v.GetAircraft(callsign); ac == nil {
		return ErrNoAircraftForCallsign
	} else if v.trackedByAnotherController(callsign) {
		return ErrOtherControllerHasTrack
	} else {
		ac.assignedSquawk = code
		controlUpdates.modifiedAircraft[ac] = nil
		v.controlDelegate.SetSquawk(callsign, code)
		return nil
	}
}

func (v *VATSIMServer) SetSquawkAutomatic(callsign string) error {
	if !v.atcValid {
		return ErrNotController
	} else if ac := v.GetAircraft(callsign); ac == nil {
		return ErrNoAircraftForCallsign
	} else if ac.flightPlan == nil {
		return ErrNoFlightPlanFiled
	} else if ac.flightPlan.rules != IFR {
		return errors.New("non-IFR squawk codes must be set manually")
	} else {
		if c, ok := v.controllers[v.callsign]; !ok {
			lg.Errorf("%s: no Controller for me?", v.callsign)
			return errors.New("Must be signed in to a control position")
		} else {
			pos := c.GetPosition()
			if pos == nil {
				return errors.New("Radio must be primed to assign squawk codes")
			}
			if pos.lowSquawk == pos.highSquawk {
				return errors.New("Current position has not been assigned a squawk code range")
			}

			squawkUnused := func(sq Squawk) bool {
				for _, ac := range v.aircraft {
					if ac.assignedSquawk == sq {
						return false
					}
				}
				return true
			}

			// Start at a random point in the range and then go linearly from
			// there.
			n := int(pos.highSquawk - pos.lowSquawk)
			offset := rand.Int() % n
			for i := 0; i < n; i++ {
				sq := pos.lowSquawk + Squawk((i+offset)%n)
				if squawkUnused(sq) {
					return v.SetSquawk(callsign, sq)
				}
			}
			return fmt.Errorf("No free squawk codes between %s and %s(!)",
				pos.lowSquawk, pos.highSquawk)
		}
	}
}

func (v *VATSIMServer) SetScratchpad(callsign string, scratchpad string) error {
	if !v.atcValid {
		return ErrNotController
	} else if ac := v.GetAircraft(callsign); ac == nil {
		return ErrNoAircraftForCallsign
	} else if v.trackedByAnotherController(callsign) {
		return ErrOtherControllerHasTrack
	} else if len(scratchpad) > 3 {
		return ErrScratchpadTooLong
	} else {
		ac.scratchpad = scratchpad
		controlUpdates.modifiedAircraft[ac] = nil
		v.controlDelegate.SetScratchpad(callsign, scratchpad)
		return nil
	}
}

func (v *VATSIMServer) SetTemporaryAltitude(callsign string, altitude int) error {
	if !v.atcValid {
		return ErrNotController
	} else if ac := v.GetAircraft(callsign); ac == nil {
		return ErrNoAircraftForCallsign
	} else if v.trackedByAnotherController(callsign) {
		return ErrOtherControllerHasTrack
	} else {
		ac.tempAltitude = altitude
		controlUpdates.modifiedAircraft[ac] = nil
		v.controlDelegate.SetTemporaryAltitude(callsign, altitude)
		return nil
	}
}

func (v *VATSIMServer) SetVoiceType(callsign string, voice string) error {
	if !v.atcValid {
		return ErrNotController
	} else if ac := v.GetAircraft(callsign); ac == nil {
		return ErrNoAircraftForCallsign
	} else if v.trackedByAnotherController(callsign) {
		return ErrOtherControllerHasTrack
	} else {
		voice = strings.ToLower(voice)

		switch voice {
		case "v":
			ac.voiceCapability = Voice
		case "r":
			ac.voiceCapability = Receive
		case "t":
			ac.voiceCapability = Text
		default:
			return errors.New("Invalid voice communications type specified")
		}

		v.controlDelegate.SetVoiceType(callsign, voice)

		return amendFlightPlan(callsign, func(fp *FlightPlan) {
			voiceStr := "/" + voice + "/"
			// Is the voice type already in the remarks?
			if strings.Contains(fp.remarks, voiceStr) {
				return
			}

			// Remove any existing voice type
			fp.remarks = strings.ReplaceAll(fp.remarks, "/v/", "")
			fp.remarks = strings.ReplaceAll(fp.remarks, "/r/", "")
			fp.remarks = strings.ReplaceAll(fp.remarks, "/t/", "")

			// And insert the one that was set
			fp.remarks += " " + voiceStr
		})
	}
}

func (v *VATSIMServer) AmendFlightPlan(callsign string, fp FlightPlan) error {
	if !v.atcValid {
		return ErrNotController
	} else if ac := v.GetAircraft(callsign); ac == nil {
		return ErrNoAircraftForCallsign
	} else if v.trackedByAnotherController(callsign) {
		return ErrOtherControllerHasTrack
	} else if ac.flightPlan == nil {
		return ErrNoFlightPlanFiled
	} else {
		ac.flightPlan = &fp
		controlUpdates.modifiedAircraft[ac] = nil
		v.controlDelegate.AmendFlightPlan(callsign, fp)
		return nil
	}
}

func (v *VATSIMServer) PushFlightStrip(fs FlightStrip, controller string) error {
	return nil
}

func (v *VATSIMServer) InitiateTrack(callsign string) error {
	if !v.atcValid {
		return ErrNotController
	} else if ac := v.GetAircraft(callsign); ac == nil {
		return ErrNoAircraftForCallsign
	} else if v.trackedByAnotherController(callsign) {
		return ErrOtherControllerHasTrack
	} else {
		v.trackingControllers[callsign] = v.callsign
		controlUpdates.modifiedAircraft[ac] = nil
		v.controlDelegate.InitiateTrack(callsign)
		return nil
	}
}

func (v *VATSIMServer) DropTrack(callsign string) error {
	if !v.atcValid {
		return ErrNotController
	} else if ac := v.GetAircraft(callsign); ac == nil {
		return ErrNoAircraftForCallsign
	} else if v.trackedByAnotherController(callsign) {
		return ErrOtherControllerHasTrack
	} else {
		delete(v.trackingControllers, callsign)
		controlUpdates.modifiedAircraft[ac] = nil
		v.controlDelegate.DropTrack(callsign)
		return nil
	}
}

func (v *VATSIMServer) Handoff(callsign string, controller string) error {
	if !v.atcValid {
		return ErrNotController
	} else if ac := v.GetAircraft(callsign); ac == nil {
		return ErrNoAircraftForCallsign
	} else if v.trackedByAnotherController(callsign) {
		return ErrOtherControllerHasTrack
	} else if c := v.GetController(controller); c == nil {
		return ErrNoController
	} else {
		v.outboundHandoffs[callsign] = controller
		controlUpdates.modifiedAircraft[ac] = nil
		v.controlDelegate.Handoff(callsign, controller)
		return nil
	}
}

func (v *VATSIMServer) AcceptHandoff(callsign string) error {
	if !v.atcValid {
		return ErrNotController
	} else if ac := v.GetAircraft(callsign); ac == nil {
		return ErrNoAircraftForCallsign
	} else if _, ok := v.inboundHandoffs[callsign]; !ok {
		return ErrNotBeingHandedOffToMe
	} else {
		v.trackingControllers[callsign] = v.callsign
		controlUpdates.modifiedAircraft[ac] = nil
		v.controlDelegate.AcceptHandoff(callsign)
		delete(v.inboundHandoffs, callsign)
		return nil
	}
}

func (v *VATSIMServer) RejectHandoff(callsign string) error {
	if !v.atcValid {
		return ErrNotController
	} else if ac := v.GetAircraft(callsign); ac == nil {
		return ErrNoAircraftForCallsign
	} else if _, ok := v.inboundHandoffs[callsign]; !ok {
		return ErrNotBeingHandedOffToMe
	} else {
		controlUpdates.modifiedAircraft[ac] = nil
		v.controlDelegate.RejectHandoff(callsign)
		delete(v.inboundHandoffs, callsign)
		return nil
	}
}

func (v *VATSIMServer) PointOut(callsign string, controller string) error {
	if !v.atcValid {
		return ErrNotController
	} else if ac := v.GetAircraft(callsign); ac == nil {
		return ErrNoAircraftForCallsign
	} else if v.trackedByAnotherController(callsign) {
		return ErrOtherControllerHasTrack
	} else if c := v.GetController(controller); c == nil {
		return ErrNoController
	} else {
		v.controlDelegate.PointOut(callsign, controller)
		return nil
	}
}

func (v *VATSIMServer) SendTextMessage(m TextMessage) error {
	// TODO: can observers send any kind of text message? Maybe private?
	if !v.atcValid {
		return ErrNotController
	}
	if globalConfig.VatsimRating != SupervisorRating &&
		globalConfig.VatsimRating != AdministratorRating &&
		m.messageType == TextBroadcast {
		return errors.New("Broadcast messages cannot be sent by non-supervisors")
	}

	v.controlDelegate.SendTextMessage(m)
	return nil
}

func (v *VATSIMServer) GetUpdates() {
	if v.connection != nil {
		// Receive messages here; this runs in the same thread as the GUI et
		// al., so there's nothing to worry about w.r.t. races.
		messages := v.connection.GetMessages()
		for _, msg := range messages {
			strs := strings.Split(strings.TrimSpace(msg.Contents), ":")
			if len(strs) == 0 {
				lg.Printf("vatsim: empty message received?")
				continue
			}
			if len(strs[0]) == 0 {
				lg.Printf("vatsim: empty first field? \"%s\"", msg.Contents)
				continue
			}

			if *logTraffic {
				lg.Printf("Received: %s", msg.Contents)
			}

			matches := 0
			for _, spec := range vatsimMessageSpecs {
				if sender, args, ok := spec.Match(strs); ok {
					matches++
					if spec.handler != nil {
						if err := spec.handler(v, sender, args); err != nil {
							lg.Printf("FSD message error: %T: %s: %s", err, err, msg.Contents)
						}
					}
				}
			}
			if matches == 0 {
				lg.Printf("No rule matched: %s", msg.Contents)
			}
		}

		// Do this after processing the messages.
		if vatsimUpdateCallback != nil {
			vatsimUpdateCallback(v)
		}
	}

	// Clean up anyone who we haven't heard from in 30 minutes
	now := v.CurrentTime()
	for callsign, ac := range v.aircraft {
		if now.Sub(ac.tracks[0].time).Minutes() > 30. {
			delete(v.aircraft, callsign)
			delete(v.trackingControllers, callsign)
			delete(v.outboundHandoffs, callsign)
			delete(v.inboundHandoffs, callsign)

			controlUpdates.RemoveAircraft(ac)
		}
	}
}

func (v *VATSIMServer) Disconnect() {
	if v.connection == nil {
		return
	}

	v.connection.Close()
	v.connection = nil
	v.windowTitle = " [Disconnected]"
	v.controlDelegate = &InertAircraftController{}

	for _, ac := range v.aircraft {
		controlUpdates.removedAircraft[ac] = nil
	}

	v.aircraft = make(map[string]*Aircraft)
	v.users = make(map[string]*User)
	v.pilots = make(map[string]*Pilot)
	v.controllers = make(map[string]*Controller)
	v.metar = make(map[string]METAR)
	v.atis = make(map[string]string)
	v.trackingControllers = make(map[string]string)
	v.outboundHandoffs = make(map[string]string)
	v.inboundHandoffs = make(map[string]string)
}

func (v *VATSIMServer) Connected() bool {
	return v.connection != nil
}

func (v *VATSIMServer) Callsign() string {
	return v.callsign
}

func (v *VATSIMServer) CurrentTime() time.Time {
	if v.connection == nil {
		return time.Time{}
	}
	return v.connection.CurrentTime()
}

func (v *VATSIMServer) GetWindowTitle() string {
	return v.windowTitle
}

func (v *VATSIMServer) trackedByAnotherController(callsign string) bool {
	c, ok := v.trackingControllers[callsign]
	return ok && c != v.callsign
}

func (v *VATSIMServer) send(fields ...interface{}) {
	if v.connection != nil {
		v.connection.SendMessage(v.callsign, fields...)
	} else {
		lg.Printf("Tried to send message to closed connection")
	}
}

func (v *VATSIMServer) getOrCreateAircraft(callsign string) *Aircraft {
	if ac, ok := v.aircraft[callsign]; ok {
		controlUpdates.modifiedAircraft[ac] = nil
		return ac
	} else {
		ac = &Aircraft{callsign: callsign}
		v.aircraft[callsign] = ac
		controlUpdates.addedAircraft[ac] = nil
		return ac
	}
}

func amendFlightPlan(callsign string, amend func(fp *FlightPlan)) error {
	if ac := server.GetAircraft(callsign); ac == nil {
		return ErrNoAircraftForCallsign
	} else {
		if ac.flightPlan == nil {
			ac.flightPlan = &FlightPlan{}
		}
		amend(ac.flightPlan)
		return server.AmendFlightPlan(callsign, *ac.flightPlan)
	}
}

func (v *VATSIMServer) trackInitiated(callsign string, controller string) error {
	// Sometimes we get this message for a/c we haven't seen. Don't worry
	// about it when it happens...
	if ac := v.GetAircraft(callsign); ac != nil {
		if tc, ok := v.trackingControllers[callsign]; ok && tc != controller {
			// This seems to happen for far-away aircraft where we don't
			// see all of the messages..
			// lg.Printf("%s: %s is tracking controller but %s initiated track?", callsign, tc, controller)
		}
		v.trackingControllers[callsign] = controller
		controlUpdates.modifiedAircraft[ac] = nil
	}
	return nil
}