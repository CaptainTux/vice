// sim.go
// Copyright(c) 2022 Matt Pharr, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/mmp/imgui-go/v4"
)

const initialSimSeconds = 45

var (
	ErrArrivalAirportUnknown        = errors.New("Arrival airport unknown")
	ErrUnknownApproach              = errors.New("Unknown approach")
	ErrClearedForUnexpectedApproach = errors.New("Cleared for unexpected approach")
	ErrNoAircraftForCallsign        = errors.New("No aircraft exists with specified callsign")
	ErrNoFlightPlan                 = errors.New("No flight plan has been filed for aircraft")
	ErrOtherControllerHasTrack      = errors.New("Another controller is already tracking the aircraft")
	ErrNotBeingHandedOffToMe        = errors.New("Aircraft not being handed off to current controller")
	ErrNoController                 = errors.New("No controller with that callsign")
	ErrUnknownAircraftType          = errors.New("Unknown aircraft type")
	ErrUnableCommand                = errors.New("Unable")
)

type SimConnectionConfiguration struct {
	departureChallenge float32
	goAroundRate       float32
	scenario           *Scenario
	controller         *Controller
	validControllers   map[string]*Controller

	// airport -> runway -> category -> rate
	departureRates map[string]map[string]map[string]*int32
	// arrival group -> airport -> rate
	arrivalGroupRates map[string]map[string]*int32
}

func (ssc *SimConnectionConfiguration) Initialize() {
	ssc.departureChallenge = 0.25
	ssc.goAroundRate = 0.10
	ssc.ResetScenarioGroup()
}

func (ssc *SimConnectionConfiguration) ResetScenarioGroup() {
	ssc.validControllers = make(map[string]*Controller)
	for _, sc := range scenarioGroup.Scenarios {
		ssc.validControllers[sc.Callsign] = scenarioGroup.ControlPositions[sc.Callsign]
	}
	ssc.controller = scenarioGroup.ControlPositions[scenarioGroup.DefaultController]

	ssc.SetScenario(scenarioGroup.DefaultScenarioGroup)

	globalConfig.DisplayRoot.VisitPanes(func(p Pane) {
		if stars, ok := p.(*STARSPane); ok {
			stars.ResetScenarioGroup()
			stars.ResetScenario(ssc.scenario)
		}
	})
}

func (ssc *SimConnectionConfiguration) SetScenario(name string) {
	var ok bool
	ssc.scenario, ok = scenarioGroup.Scenarios[name]
	if !ok {
		lg.Errorf("%s: called SetScenario with an unknown scenario name???", name)
		return
	}

	ssc.arrivalGroupRates = DuplicateMap(ssc.scenario.ArrivalGroupDefaultRates)

	ssc.departureRates = make(map[string]map[string]map[string]*int32)
	for _, rwy := range ssc.scenario.DepartureRunways {
		if _, ok := ssc.departureRates[rwy.Airport]; !ok {
			ssc.departureRates[rwy.Airport] = make(map[string]map[string]*int32)
		}
		if _, ok := ssc.departureRates[rwy.Airport][rwy.Runway]; !ok {
			ssc.departureRates[rwy.Airport][rwy.Runway] = make(map[string]*int32)
		}
		ssc.departureRates[rwy.Airport][rwy.Runway][rwy.Category] = new(int32)
		*ssc.departureRates[rwy.Airport][rwy.Runway][rwy.Category] = rwy.DefaultRate
	}

	globalConfig.DisplayRoot.VisitPanes(func(p Pane) {
		if stars, ok := p.(*STARSPane); ok {
			stars.ResetScenario(ssc.scenario)
		}
	})
}

func (ssc *SimConnectionConfiguration) DrawUI() bool {
	if imgui.BeginComboV("Scenario Group", scenarioGroup.Name, imgui.ComboFlagsHeightLarge) {
		for _, name := range SortedMapKeys(scenarioGroups) {
			if imgui.SelectableV(name, name == scenarioGroup.Name, 0, imgui.Vec2{}) {
				scenarioGroup = scenarioGroups[name]
				globalConfig.LastScenarioGroup = name
				ssc.ResetScenarioGroup()
			}
		}
		imgui.EndCombo()
	}

	if imgui.BeginComboV("Control Position", ssc.controller.Callsign, imgui.ComboFlagsHeightLarge) {
		for _, controllerName := range SortedMapKeys(ssc.validControllers) {
			if imgui.SelectableV(controllerName, controllerName == ssc.controller.Callsign, 0, imgui.Vec2{}) {
				ssc.controller = ssc.validControllers[controllerName]
				// Set the current scenario to the first one alphabetically
				// with the selected controller.
				for _, scenarioName := range SortedMapKeys(scenarioGroup.Scenarios) {
					if scenarioGroup.Scenarios[scenarioName].Callsign == controllerName {
						ssc.SetScenario(scenarioName)
						break
					}
				}
			}
		}
		imgui.EndCombo()
	}

	scenario := ssc.scenario

	if imgui.BeginComboV("Config", scenario.Name(), imgui.ComboFlagsHeightLarge) {
		for _, name := range SortedMapKeys(scenarioGroup.Scenarios) {
			if scenarioGroup.Scenarios[name].Callsign != ssc.controller.Callsign {
				continue
			}
			if imgui.SelectableV(name, name == scenario.Name(), 0, imgui.Vec2{}) {
				ssc.SetScenario(name)
			}
		}
		imgui.EndCombo()
	}

	if imgui.BeginTableV("scenario", 2, 0, imgui.Vec2{500, 0}, 0.) {
		imgui.TableNextRow()
		imgui.TableNextColumn()

		if len(ssc.departureRates) > 0 {
			imgui.TableNextRow()
			imgui.TableNextColumn()
			imgui.Text("Departing:")
			imgui.TableNextColumn()

			var runways []string
			for airport, runwayRates := range ssc.departureRates {
				for runway, categoryRates := range runwayRates {
					for _, rate := range categoryRates {
						if *rate > 0 {
							runways = append(runways, airport+"/"+runway)
							break
						}
					}
				}
			}
			sort.Strings(runways)
			imgui.Text(strings.Join(runways, ", "))
		}

		if len(scenario.ArrivalRunways) > 0 {
			imgui.TableNextRow()
			imgui.TableNextColumn()
			imgui.Text("Landing:")
			imgui.TableNextColumn()

			var a []string
			for _, rwy := range scenario.ArrivalRunways {
				a = append(a, rwy.Airport+"/"+rwy.Runway)
			}
			sort.Strings(a)
			imgui.Text(strings.Join(a, ", "))
		}

		imgui.TableNextRow()
		imgui.TableNextColumn()
		imgui.Text("Wind:")
		imgui.TableNextColumn()
		if scenario.Wind.Gust > scenario.Wind.Speed {
			imgui.Text(fmt.Sprintf("%d at %d gust %d", scenario.Wind.Direction, scenario.Wind.Speed, scenario.Wind.Gust))
		} else {
			imgui.Text(fmt.Sprintf("%d at %d", scenario.Wind.Direction, scenario.Wind.Speed))
		}
		imgui.EndTable()
	}

	if len(scenario.DepartureRunways) > 0 {
		imgui.Separator()
		imgui.Text("Departures")

		sumRates := 0
		for _, runwayRates := range ssc.departureRates {
			for _, categoryRates := range runwayRates {
				for _, rate := range categoryRates {
					sumRates += int(*rate)
				}
			}
		}
		imgui.Text(fmt.Sprintf("Overall departure rate: %d / hour", sumRates))

		imgui.SliderFloatV("Sequencing challenge", &ssc.departureChallenge, 0, 1, "%.02f", 0)
		flags := imgui.TableFlagsBordersV | imgui.TableFlagsBordersOuterH | imgui.TableFlagsRowBg | imgui.TableFlagsSizingStretchProp

		if imgui.BeginTableV("departureRunways", 4, flags, imgui.Vec2{500, 0}, 0.) {
			imgui.TableSetupColumn("Airport")
			imgui.TableSetupColumn("Runway")
			imgui.TableSetupColumn("Category")
			imgui.TableSetupColumn("ADR")
			imgui.TableHeadersRow()

			for _, airport := range SortedMapKeys(ssc.departureRates) {
				imgui.PushID(airport)
				for _, runway := range SortedMapKeys(ssc.departureRates[airport]) {
					imgui.PushID(runway)
					for _, category := range SortedMapKeys(ssc.departureRates[airport][runway]) {
						rate := ssc.departureRates[airport][runway][category]
						imgui.PushID(category)

						imgui.TableNextRow()
						imgui.TableNextColumn()
						imgui.Text(airport)
						imgui.TableNextColumn()
						imgui.Text(runway)
						imgui.TableNextColumn()
						if category == "" {
							imgui.Text("(All)")
						} else {
							imgui.Text(category)
						}
						imgui.TableNextColumn()
						imgui.InputIntV("##adr", rate, 0, 120, 0)

						imgui.PopID()
					}
					imgui.PopID()
				}
				imgui.PopID()
			}
			imgui.EndTable()
		}
	}

	if len(ssc.arrivalGroupRates) > 0 {
		// Figure out how many unique airports we've got for AAR columns in the table
		// and also sum up the overall arrival rate
		allAirports := make(map[string]interface{})
		sumRates := 0
		for _, agr := range ssc.arrivalGroupRates {
			for ap, rate := range agr {
				allAirports[ap] = nil
				sumRates += int(*rate)
			}
		}
		nAirports := len(allAirports)

		imgui.Separator()
		imgui.Text("Arrivals")
		imgui.Text(fmt.Sprintf("Overall arrival rate: %d / hour", sumRates))
		imgui.SliderFloatV("Go around probability", &ssc.goAroundRate, 0, 1, "%.02f", 0)

		flags := imgui.TableFlagsBordersV | imgui.TableFlagsBordersOuterH | imgui.TableFlagsRowBg | imgui.TableFlagsSizingStretchProp
		if imgui.BeginTableV("arrivalgroups", 1+nAirports, flags, imgui.Vec2{500, 0}, 0.) {
			imgui.TableSetupColumn("Arrival")
			sortedAirports := SortedMapKeys(allAirports)
			for _, ap := range sortedAirports {
				imgui.TableSetupColumn(ap + " AAR")
			}
			imgui.TableHeadersRow()

			for _, group := range SortedMapKeys(ssc.arrivalGroupRates) {
				imgui.PushID(group)
				imgui.TableNextRow()
				imgui.TableNextColumn()
				imgui.Text(group)
				for _, ap := range sortedAirports {
					imgui.TableNextColumn()
					if rate, ok := ssc.arrivalGroupRates[group][ap]; ok {
						imgui.InputIntV("##aar-"+ap, rate, 0, 120, 0)
					}
				}
				imgui.PopID()
			}
			imgui.EndTable()
		}
	}

	return false
}

func (ssc *SimConnectionConfiguration) Valid() bool {
	return true
}

func (ssc *SimConnectionConfiguration) Connect() error {
	// Send out events to remove any existing aircraft (necessary for when
	// we restart...)
	for _, ac := range sim.GetAllAircraft() {
		eventStream.Post(&RemovedAircraftEvent{ac: ac})
	}
	sim.Disconnect()
	sim = NewSim(*ssc)
	sim.Prespawn()
	return nil
}

///////////////////////////////////////////////////////////////////////////
// Sim

type Sim struct {
	Scenario *Scenario

	Aircraft map[string]*Aircraft
	Handoffs map[string]time.Time
	METAR    map[string]*METAR

	SerializeTime time.Time // for updating times on deserialize

	currentTime    time.Time // this is our fake time--accounting for pauses & simRate..
	lastUpdateTime time.Time // this is w.r.t. true wallclock time
	SimRate        float32
	Paused         bool

	eventsId EventSubscriberId

	DepartureChallenge float32
	GoAroundRate       float32
	WillGoAround       map[string]interface{}

	lastTrackUpdate time.Time
	lastSimUpdate   time.Time

	showSettings bool

	// airport -> runway -> category -> rate
	DepartureRates map[string]map[string]map[string]*int32
	// arrival group -> airport -> rate
	ArrivalGroupRates map[string]map[string]*int32

	// The same runway may be present multiple times in DepartureRates,
	// with different categories. However, we want to make sure that we
	// don't spawn two aircraft on the same runway at the same time (or
	// close to it).  Therefore, here we track a per-runway "when's the
	// next time that we will spawn *something* from the runway" time.
	// When the time is up, we'll figure out which specific category to
	// use...
	// airport -> runway -> time
	NextDepartureSpawn map[string]map[string]time.Time

	// Key is arrival group name
	NextArrivalSpawn map[string]time.Time
}

func NewSim(ssc SimConnectionConfiguration) *Sim {
	rand.Seed(time.Now().UnixNano())

	sim := &Sim{
		Scenario: ssc.scenario,

		Aircraft: make(map[string]*Aircraft),
		Handoffs: make(map[string]time.Time),
		METAR:    make(map[string]*METAR),

		DepartureRates:    DuplicateMap(ssc.departureRates),
		ArrivalGroupRates: DuplicateMap(ssc.arrivalGroupRates),

		currentTime:        time.Now(),
		lastUpdateTime:     time.Now(),
		eventsId:           eventStream.Subscribe(),
		SimRate:            1,
		DepartureChallenge: ssc.departureChallenge,
		GoAroundRate:       ssc.goAroundRate,
		WillGoAround:       make(map[string]interface{}),
	}

	// Make some fake METARs; slightly different for all airports.
	alt := 2980 + rand.Intn(40)
	for _, ap := range sim.Scenario.AllAirports() {
		spd := sim.Scenario.Wind.Speed - 3 + rand.Int31n(6)
		var wind string
		if spd < 0 {
			wind = "00000KT"
		} else if spd < 4 {
			wind = fmt.Sprintf("VRB%02dKT", spd)
		} else {
			dir := 10 * ((sim.Scenario.Wind.Direction + 5) / 10)
			dir += [3]int32{-10, 0, 10}[rand.Intn(3)]
			wind = fmt.Sprintf("%03d%02d", dir, spd)
			gst := sim.Scenario.Wind.Gust - 3 + rand.Int31n(6)
			if gst-sim.Scenario.Wind.Speed > 5 {
				wind += fmt.Sprintf("G%02d", gst)
			}
			wind += "KT"
		}

		// Just provide the stuff that the STARS display shows
		sim.METAR[ap] = &METAR{
			AirportICAO: ap,
			Wind:        wind,
			Altimeter:   fmt.Sprintf("A%d", alt-2+rand.Intn(4)),
		}
	}

	sim.SetInitialSpawnTimes()

	return sim
}

func (sim *Sim) SetInitialSpawnTimes() {
	// Randomize next spawn time for departures and arrivals; may be before
	// or after the current time.
	randomSpawn := func(rate int) time.Time {
		if rate == 0 {
			return time.Now().Add(365 * 24 * time.Hour)
		}
		avgWait := 3600 / rate
		delta := rand.Intn(avgWait) - avgWait/2 - initialSimSeconds
		return time.Now().Add(time.Duration(delta) * time.Second)
	}

	sim.NextArrivalSpawn = make(map[string]time.Time)
	for group, rates := range sim.ArrivalGroupRates {
		rateSum := 0
		for _, rate := range rates {
			rateSum += int(*rate)
		}
		sim.NextArrivalSpawn[group] = randomSpawn(rateSum)
	}

	sim.NextDepartureSpawn = make(map[string]map[string]time.Time)
	for airport, runwayRates := range sim.DepartureRates {
		spawn := make(map[string]time.Time)

		for runway, categoryRates := range runwayRates {
			rateSum := 0
			for _, rate := range categoryRates {
				rateSum += int(*rate)
			}
			if rateSum > 0 {
				spawn[runway] = randomSpawn(rateSum)
			}
		}

		if len(spawn) > 0 {
			sim.NextDepartureSpawn[airport] = spawn
		}
	}
}

func (sim *Sim) Prespawn() {
	// Prime the pump before the user gets involved
	t := time.Now().Add(-(initialSimSeconds + 1) * time.Second)
	for i := 0; i < initialSimSeconds; i++ {
		sim.currentTime = t
		sim.lastUpdateTime = t
		t = t.Add(1 * time.Second)

		sim.updateState()
	}
	sim.currentTime = time.Now()
	sim.lastUpdateTime = time.Now()
}

func (sim *Sim) SetSquawk(callsign string, squawk Squawk) error {
	return nil // UNIMPLEMENTED
}

func (sim *Sim) SetSquawkAutomatic(callsign string) error {
	return nil // UNIMPLEMENTED
}

func (sim *Sim) SetScratchpad(callsign string, scratchpad string) error {
	if ac, ok := sim.Aircraft[callsign]; !ok {
		return ErrNoAircraftForCallsign
	} else if ac.TrackingController != sim.Scenario.Callsign {
		return ErrOtherControllerHasTrack
	} else {
		ac.Scratchpad = scratchpad
		eventStream.Post(&ModifiedAircraftEvent{ac: ac})
		return nil
	}
}

func (sim *Sim) SetTemporaryAltitude(callsign string, alt int) error {
	return nil // UNIMPLEMENTED
}

func (sim *Sim) AmendFlightPlan(callsign string, fp FlightPlan) error {
	return nil // UNIMPLEMENTED
}

func (sim *Sim) PushFlightStrip(callsign string, controller string) error {
	return nil // UNIMPLEMENTED
}

func (sim *Sim) InitiateTrack(callsign string) error {
	if ac, ok := sim.Aircraft[callsign]; !ok {
		return ErrNoAircraftForCallsign
	} else if ac.TrackingController != "" {
		return ErrOtherControllerHasTrack
	} else {
		ac.TrackingController = sim.Scenario.Callsign
		eventStream.Post(&ModifiedAircraftEvent{ac: ac})
		eventStream.Post(&InitiatedTrackEvent{ac: ac})
		return nil
	}
}

func (sim *Sim) DropTrack(callsign string) error {
	if ac, ok := sim.Aircraft[callsign]; !ok {
		return ErrNoAircraftForCallsign
	} else if ac.TrackingController != sim.Scenario.Callsign {
		return ErrOtherControllerHasTrack
	} else {
		ac.TrackingController = ""
		eventStream.Post(&ModifiedAircraftEvent{ac: ac})
		eventStream.Post(&DroppedTrackEvent{ac: ac})
		return nil
	}
}

func (sim *Sim) Handoff(callsign string, controller string) error {
	if ac, ok := sim.Aircraft[callsign]; !ok {
		return ErrNoAircraftForCallsign
	} else if ac.TrackingController != sim.Scenario.Callsign {
		return ErrOtherControllerHasTrack
	} else if ctrl := sim.GetController(controller); ctrl == nil {
		return ErrNoController
	} else {
		ac.OutboundHandoffController = ctrl.Callsign
		eventStream.Post(&ModifiedAircraftEvent{ac: ac})
		acceptDelay := 2 + rand.Intn(10)
		sim.Handoffs[callsign] = sim.CurrentTime().Add(time.Duration(acceptDelay) * time.Second)
		return nil
	}
}

func (sim *Sim) AcceptHandoff(callsign string) error {
	if ac, ok := sim.Aircraft[callsign]; !ok {
		return ErrNoAircraftForCallsign
	} else if ac.InboundHandoffController != sim.Scenario.Callsign {
		return ErrNotBeingHandedOffToMe
	} else {
		ac.InboundHandoffController = ""
		ac.TrackingController = sim.Scenario.Callsign
		eventStream.Post(&AcceptedHandoffEvent{controller: sim.Scenario.Callsign, ac: ac})
		eventStream.Post(&ModifiedAircraftEvent{ac: ac}) // FIXME...
		return nil
	}
}

func (sim *Sim) RejectHandoff(callsign string) error {
	return nil // UNIMPLEMENTED
}

func (sim *Sim) CancelHandoff(callsign string) error {
	if ac, ok := sim.Aircraft[callsign]; !ok {
		return ErrNoAircraftForCallsign
	} else if ac.TrackingController != sim.Scenario.Callsign {
		return ErrOtherControllerHasTrack
	} else {
		ac.OutboundHandoffController = ""
		// TODO: we are inconsistent in other control backends about events
		// when user does things like this; sometimes no event, sometimes
		// modified a/c event...
		eventStream.Post(&ModifiedAircraftEvent{ac: ac})
		return nil
	}
}

func (sim *Sim) PointOut(callsign string, controller string) error {
	return nil // UNIMPLEMENTED
}

func (sim *Sim) RequestControllerATIS(controller string) error {
	return nil // UNIMPLEMENTED
}

func (sim *Sim) SetRadarCenters(primary Point2LL, secondary [3]Point2LL, rangeNm int) error {
	return nil // UNIMPLEMENTED
}

func (sim *Sim) Disconnect() {
	for _, ac := range sim.Aircraft {
		eventStream.Post(&RemovedAircraftEvent{ac: ac})
	}
	if sim.eventsId != InvalidEventSubscriberId {
		eventStream.Unsubscribe(sim.eventsId)
		sim.eventsId = InvalidEventSubscriberId
	}
}

func (sim *Sim) GetAircraft(callsign string) *Aircraft {
	if ac, ok := sim.Aircraft[callsign]; ok {
		return ac
	}
	return nil
}

func (sim *Sim) GetFilteredAircraft(filter func(*Aircraft) bool) []*Aircraft {
	var filtered []*Aircraft
	for _, ac := range sim.Aircraft {
		if filter(ac) {
			filtered = append(filtered, ac)
		}
	}
	return filtered
}

func (sim *Sim) GetAllAircraft() []*Aircraft {
	return sim.GetFilteredAircraft(func(*Aircraft) bool { return true })
}

func (sim *Sim) GetFlightStrip(callsign string) *FlightStrip {
	if ac, ok := sim.Aircraft[callsign]; ok {
		return &ac.Strip
	}
	return nil
}

func (sim *Sim) AddAirportForWeather(airport string) {
	// UNIMPLEMENTED
}

func (sim *Sim) GetMETAR(location string) *METAR {
	return sim.METAR[location]
}

func (sim *Sim) GetAirportATIS(airport string) []ATIS {
	// UNIMPLEMENTED
	return nil
}

func (sim *Sim) GetController(callsign string) *Controller {
	if sim.Scenario == nil {
		return nil
	}

	ctrl, ok := scenarioGroup.ControlPositions[callsign]
	if ok {
		return ctrl
	}

	for _, c := range scenarioGroup.ControlPositions {
		// Make sure that the controller is active in the scenarioGroup...
		if c.SectorId == callsign && Find(sim.Scenario.Controllers, c.Callsign) != -1 {
			return c
		}
	}

	return ctrl
}

func (sim *Sim) GetAllControllers() []*Controller {
	if sim.Scenario == nil {
		return nil
	}

	_, ctrl := FlattenMap(scenarioGroup.ControlPositions)
	return FilterSlice(ctrl,
		func(ctrl *Controller) bool { return Find(sim.Scenario.Controllers, ctrl.Callsign) != -1 })
}

func (sim *Sim) SetPrimaryFrequency(f Frequency) {
	// UNIMPLEMENTED
}

func (sim *Sim) GetUpdates() {
	if sim.Paused || sim.Scenario == nil {
		return
	}

	// Process events
	if sim.eventsId != InvalidEventSubscriberId {
		for _, ev := range eventStream.Get(sim.eventsId) {
			if rem, ok := ev.(*RemovedAircraftEvent); ok {
				delete(sim.Aircraft, rem.ac.Callsign)
			}
		}
	}

	// Update the current time
	elapsed := time.Since(sim.lastUpdateTime)
	elapsed = time.Duration(sim.SimRate * float32(elapsed))
	sim.currentTime = sim.currentTime.Add(elapsed)
	sim.lastUpdateTime = time.Now()

	sim.updateState()
}

// FIXME: this is poorly named...
func (sim *Sim) updateState() {
	// Accept any handoffs whose time has time...
	now := sim.CurrentTime()
	for callsign, t := range sim.Handoffs {
		if now.After(t) {
			if ac, ok := sim.Aircraft[callsign]; ok {
				ac.TrackingController = ac.OutboundHandoffController
				ac.OutboundHandoffController = ""
				eventStream.Post(&AcceptedHandoffEvent{controller: ac.TrackingController, ac: ac})
				globalConfig.Audio.PlaySound(AudioEventHandoffAccepted)

				// Climb to cruise altitude...
				ac.AssignedAltitude = ac.FlightPlan.Altitude
			}
			delete(sim.Handoffs, callsign)
		}
	}

	// Update the simulation state once a second.
	if now.Sub(sim.lastSimUpdate) >= time.Second {
		sim.lastSimUpdate = now
		for _, ac := range sim.Aircraft {
			ac.Update()

			if _, ok := sim.WillGoAround[ac.Callsign]; !ok {
				continue
			}

			if !ac.OnFinal || len(ac.Waypoints) != 1 {
				continue
			}

			dist := nmdistance2ll(ac.Position, ac.Waypoints[0].Location)
			if dist < 0.25 {
				delete(sim.WillGoAround, ac.Callsign)
				ac.GoAround(sim)
				pilotResponse(ac.Callsign, "Going around")
			}
		}
	}

	// Add a new radar track every 5 seconds.
	if now.Sub(sim.lastTrackUpdate) >= 5*time.Second {
		sim.lastTrackUpdate = now

		for _, ac := range sim.Aircraft {
			ac.AddTrack(RadarTrack{
				Position:    ac.Position,
				Altitude:    int(ac.Altitude),
				Groundspeed: int(ac.GS),
				Heading:     ac.Heading - scenarioGroup.MagneticVariation,
				Time:        now,
			})

			eventStream.Post(&ModifiedAircraftEvent{ac: ac})
		}
	}

	sim.SpawnAircraft()
}

func (sim *Sim) Connected() bool {
	return true
}

func (sim *Sim) Callsign() string {
	if sim.Scenario != nil {
		return sim.Scenario.Callsign
	} else {
		return "(disconnected)"
	}
}

func (sim *Sim) CurrentTime() time.Time {
	return sim.currentTime
}

func (sim *Sim) GetWindowTitle() string {
	if sim.Scenario == nil {
		return "(disconnected)"
	}
	return sim.Scenario.Callsign + ": " + sim.Scenario.Name()
}

func pilotResponse(callsign string, fm string, args ...interface{}) {
	lg.Printf("%s: %s", callsign, fmt.Sprintf(fm, args...))
	eventStream.Post(&RadioTransmissionEvent{callsign: callsign, message: fmt.Sprintf(fm, args...)})
}

func (sim *Sim) AssignAltitude(callsign string, altitude int) error {
	if ac, ok := sim.Aircraft[callsign]; !ok {
		return ErrNoAircraftForCallsign
	} else {
		if float32(altitude) > ac.Altitude {
			pilotResponse(callsign, "climb and maintain %d", altitude)
		} else if float32(altitude) == ac.Altitude {
			pilotResponse(callsign, "maintain %d", altitude)
		} else {
			pilotResponse(callsign, "descend and maintain %d", altitude)
		}

		if ac.AssignedSpeed != 0 {
			ac.AssignedAltitudeAfterSpeed = altitude
		} else {
			ac.AssignedAltitude = altitude
		}
		ac.CrossingAltitude = 0
		return nil
	}
}

func (sim *Sim) AssignHeading(callsign string, heading int, turn int) error {
	if ac, ok := sim.Aircraft[callsign]; !ok {
		return ErrNoAircraftForCallsign
	} else {
		if turn > 0 {
			pilotResponse(callsign, "turn right heading %d", heading)
		} else if turn == 0 {
			pilotResponse(callsign, "fly heading %d", heading)
		} else {
			pilotResponse(callsign, "turn left heading %d", heading)
		}

		// A 0 heading shouldn't be specified, but at least cause the
		// aircraft to do what is intended, since 0 represents an
		// unassigned heading.
		if heading == 0 {
			heading = 360
		}

		ac.AssignedHeading = heading
		ac.TurnDirection = turn
		ac.ClearedApproach = false // if cleared, giving a heading cancels clearance
		return nil
	}
}

func (sim *Sim) TurnLeft(callsign string, deg int) error {
	if ac, ok := sim.Aircraft[callsign]; !ok {
		return ErrNoAircraftForCallsign
	} else {
		pilotResponse(callsign, "turn %d degrees left", deg)

		if ac.AssignedHeading == 0 {
			ac.AssignedHeading = int(ac.Heading) - deg
		} else {
			ac.AssignedHeading -= deg
		}

		if ac.AssignedHeading <= 0 {
			ac.AssignedHeading += 360
		}
		ac.TurnDirection = 0
		ac.ClearedApproach = false // if cleared, giving a heading cancels clearance
		return nil
	}
}

func (sim *Sim) TurnRight(callsign string, deg int) error {
	if ac, ok := sim.Aircraft[callsign]; !ok {
		return ErrNoAircraftForCallsign
	} else {
		pilotResponse(callsign, "turn %d degrees right", deg)

		if ac.AssignedHeading == 0 {
			ac.AssignedHeading = int(ac.Heading) + deg
		} else {
			ac.AssignedHeading += deg
		}

		if ac.AssignedHeading > 360 {
			ac.AssignedHeading -= 360
		}
		ac.TurnDirection = 0
		ac.ClearedApproach = false // if cleared, giving a heading cancels clearance
		return nil
	}
}

func (sim *Sim) AssignSpeed(callsign string, speed int) error {
	if ac, ok := sim.Aircraft[callsign]; !ok {
		return ErrNoAircraftForCallsign
	} else {
		if speed == 0 {
			pilotResponse(callsign, "cancel speed restrictions")
		} else if speed < ac.Performance.Speed.Landing {
			pilotResponse(callsign, "unable--our minimum speed is %d knots", ac.Performance.Speed.Landing)
			return ErrUnableCommand
		} else if speed > ac.Performance.Speed.Max {
			pilotResponse(callsign, "unable--our maximum speed is %d knots", ac.Performance.Speed.Max)
			return ErrUnableCommand
		} else if ac.ClearedApproach {
			pilotResponse(callsign, "%d knots until 5 mile final", speed)
		} else if speed == ac.AssignedSpeed {
			pilotResponse(callsign, "we'll maintain %d knots", speed)
		} else {
			pilotResponse(callsign, "maintain %d knots", speed)
		}

		if ac.AssignedAltitude != 0 {
			ac.AssignedSpeedAfterAltitude = speed
		} else {
			ac.AssignedSpeed = speed
		}
		ac.CrossingSpeed = 0
		return nil
	}
}

func (sim *Sim) DirectFix(callsign string, fix string) error {
	if ac, ok := sim.Aircraft[callsign]; !ok {
		return ErrNoAircraftForCallsign
	} else {
		fix = strings.ToUpper(fix)

		// Look for the fix in the waypoints in the flight plan.
		for i, wp := range ac.Waypoints {
			if fix == wp.Fix {
				ac.Waypoints = ac.Waypoints[i:]
				if len(ac.Waypoints) > 0 {
					ac.WaypointUpdate(wp)
				}
				pilotResponse(callsign, "direct %s", fix)
				return nil
			}
		}

		if ac.Approach != nil {
			for _, route := range ac.Approach.Waypoints {
				for _, wp := range route {
					if wp.Fix == fix {
						ac.Waypoints = []Waypoint{wp}
						if len(ac.Waypoints) > 0 {
							ac.WaypointUpdate(wp)
						}
						pilotResponse(callsign, "direct %s", fix)
						return nil
					}
				}
			}
		}

		return fmt.Errorf("%s: fix not found in route", fix)
	}
}

func (sim *Sim) getApproach(callsign string, approach string) (*Approach, *Aircraft, error) {
	ac, ok := sim.Aircraft[callsign]
	if !ok {
		return nil, nil, ErrNoAircraftForCallsign
	}
	fp := ac.FlightPlan
	if fp == nil {
		return nil, nil, ErrNoFlightPlan
	}

	ap, ok := scenarioGroup.Airports[fp.ArrivalAirport]
	if !ok {
		lg.Errorf("Can't find TRACON airport %s for %s approach for %s", fp.ArrivalAirport, approach, callsign)
		return nil, nil, ErrArrivalAirportUnknown
	}

	for name, appr := range ap.Approaches {
		if name == approach {
			return &appr, ac, nil
		}
	}
	return nil, nil, ErrUnknownApproach
}

func (sim *Sim) ExpectApproach(callsign string, approach string) error {
	ap, ac, err := sim.getApproach(callsign, approach)
	if err != nil {
		return err
	}

	ac.Approach = ap
	pilotResponse(callsign, "we'll expect the "+ap.FullName+" approach")

	return nil
}

func (sim *Sim) ClearedApproach(callsign string, approach string) error {
	ap, ac, err := sim.getApproach(callsign, approach)
	if err != nil {
		return err
	}

	response := ""
	if ac.Approach == nil {
		// allow it anyway...
		response = "you never told us to expect an approach, but ok, cleared " + ap.FullName
		ac.Approach = ap
	}
	if ac.Approach.FullName != ap.FullName {
		pilotResponse(callsign, "but you cleared us for the "+ac.Approach.FullName+" approach...")
		return ErrClearedForUnexpectedApproach
	}
	if ac.ClearedApproach {
		pilotResponse(callsign, "you already cleared us for the "+ap.FullName+" approach...")
		return nil
	}

	directApproachFix := false
	var remainingApproachWaypoints []Waypoint
	if ac.AssignedHeading == 0 && len(ac.Waypoints) > 0 {
		// Is the aircraft cleared direct to a waypoint on the approach?
		for _, approach := range ap.Waypoints {
			for i, wp := range approach {
				if wp.Fix == ac.Waypoints[0].Fix {
					directApproachFix = true
					if i+1 < len(approach) {
						remainingApproachWaypoints = approach[i+1:]
					}
					break
				}
			}
		}
	}

	if ac.Approach.Type == ILSApproach {
		if ac.AssignedHeading == 0 {
			if !directApproachFix {
				pilotResponse(callsign, "we need either direct or a heading to intercept")
				return nil
			} else {
				if remainingApproachWaypoints != nil {
					ac.Waypoints = append(ac.Waypoints, remainingApproachWaypoints...)
				}
			}
		}
		// If the aircraft is on a heading, there's nothing more to do for
		// now; keep flying the heading and after we intercept we'll add
		// the rest of the waypoints to the aircraft's waypoints array.
	} else {
		// RNAV
		if !directApproachFix {
			pilotResponse(callsign, "we need direct to a fix on the approach...")
			return nil
		}

		if remainingApproachWaypoints != nil {
			ac.Waypoints = append(ac.Waypoints, remainingApproachWaypoints...)
		}
	}

	// cleared approach cancels speed restrictions, but let's assume that
	// aircraft will just maintain their present speed and not immediately
	// accelerate up to 250...
	ac.AssignedSpeed = 0
	ac.CrossingSpeed = int(ac.IAS)
	ac.ClearedApproach = true

	pilotResponse(callsign, response+"cleared "+ap.FullName+" approach")

	lg.Printf("%s", spew.Sdump(ac))

	return nil
}

func (sim *Sim) PrintInfo(callsign string) error {
	if ac, ok := sim.Aircraft[callsign]; !ok {
		return ErrNoAircraftForCallsign
	} else {
		lg.Errorf("%s", spew.Sdump(ac))
		s := fmt.Sprintf("%s: current alt %f, assigned alt %d crossing alt %d",
			ac.Callsign, ac.Altitude, ac.AssignedAltitude, ac.CrossingAltitude)
		if ac.AssignedHeading != 0 {
			s += fmt.Sprintf(" heading %d", ac.AssignedHeading)
			if ac.TurnDirection != 0 {
				s += fmt.Sprintf(" turn direction %d", ac.TurnDirection)
			}
		}
		s += fmt.Sprintf(", IAS %f GS %.1f speed %d crossing speed %d",
			ac.IAS, ac.GS, ac.AssignedSpeed, ac.CrossingSpeed)

		if ac.ClearedApproach {
			s += ", cleared approach"
		}
		if ac.OnFinal {
			s += ", on final"
		}
		lg.Errorf("%s", s)
	}
	return nil
}

func (sim *Sim) DeleteAircraft(callsign string) error {
	if ac, ok := sim.Aircraft[callsign]; !ok {
		return ErrNoAircraftForCallsign
	} else {
		eventStream.Post(&RemovedAircraftEvent{ac: ac})
		delete(sim.Aircraft, callsign)
		return nil
	}
}

func (sim *Sim) IsPaused() bool {
	return sim.Paused
}

func (sim *Sim) TogglePause() {
	sim.Paused = !sim.Paused
	sim.lastUpdateTime = time.Now() // ignore time passage...
}

func (sim *Sim) ActivateSettingsWindow() {
	sim.showSettings = true
}

func (sim *Sim) DrawSettingsWindow() {
	if !sim.showSettings {
		return
	}

	imgui.BeginV("Simulation Settings", &sim.showSettings, imgui.WindowFlagsAlwaysAutoResize)

	if *devmode {
		imgui.SliderFloatV("Simulation speed", &sim.SimRate, 1, 100, "%.1f", 0)
	} else {
		imgui.SliderFloatV("Simulation speed", &sim.SimRate, 1, 10, "%.1f", 0)
	}

	if imgui.BeginComboV("UI Font Size", fmt.Sprintf("%d", globalConfig.UIFontSize), imgui.ComboFlagsHeightLarge) {
		sizes := make(map[int]interface{})
		for fontid := range fonts {
			if fontid.Name == "Roboto Regular" {
				sizes[fontid.Size] = nil
			}
		}
		for _, size := range SortedMapKeys(sizes) {
			if imgui.SelectableV(fmt.Sprintf("%d", size), size == globalConfig.UIFontSize, 0, imgui.Vec2{}) {
				globalConfig.UIFontSize = size
				ui.font = GetFont(FontIdentifier{Name: "Roboto Regular", Size: globalConfig.UIFontSize})
			}
		}
		imgui.EndCombo()
	}
	if imgui.BeginComboV("STARS DCB Font Size", fmt.Sprintf("%d", globalConfig.DCBFontSize), imgui.ComboFlagsHeightLarge) {
		sizes := make(map[int]interface{})
		for fontid := range fonts {
			if fontid.Name == "Inconsolata Condensed Regular" {
				sizes[fontid.Size] = nil
			}
		}
		for _, size := range SortedMapKeys(sizes) {
			if imgui.SelectableV(fmt.Sprintf("%d", size), size == globalConfig.DCBFontSize, 0, imgui.Vec2{}) {
				globalConfig.DCBFontSize = size
			}
		}
		imgui.EndCombo()
	}

	var fsp *FlightStripPane
	var stars *STARSPane
	globalConfig.DisplayRoot.VisitPanes(func(p Pane) {
		switch pane := p.(type) {
		case *FlightStripPane:
			fsp = pane
		case *STARSPane:
			stars = pane
		}
	})

	stars.DrawUI()

	imgui.Separator()

	if imgui.CollapsingHeader("Audio") {
		globalConfig.Audio.DrawUI()
	}
	if fsp != nil && imgui.CollapsingHeader("Flight Strips") {
		fsp.DrawUI()
	}
	if imgui.CollapsingHeader("Developer") {
		if imgui.BeginTableV("GlobalFiles", 4, 0, imgui.Vec2{}, 0) {
			imgui.TableNextRow()
			imgui.TableNextColumn()
			imgui.Text("Scenario:")
			imgui.TableNextColumn()
			imgui.Text(globalConfig.DevScenarioFile)
			imgui.TableNextColumn()
			if imgui.Button("New...##scenario") {
				ui.jsonSelectDialog = NewFileSelectDialogBox("Select JSON File", []string{".json"},
					globalConfig.DevScenarioFile, func(filename string) {
						globalConfig.DevScenarioFile = filename
						ui.jsonSelectDialog = nil
					})
				ui.jsonSelectDialog.Activate()
			}
			imgui.TableNextColumn()
			if globalConfig.DevScenarioFile != "" && imgui.Button("Clear##scenario") {
				globalConfig.DevScenarioFile = ""
			}

			imgui.TableNextRow()
			imgui.TableNextColumn()
			imgui.Text("Video maps:")
			imgui.TableNextColumn()
			imgui.Text(globalConfig.DevVideoMapFile)
			imgui.TableNextColumn()
			if imgui.Button("New...##vid") {
				ui.jsonSelectDialog = NewFileSelectDialogBox("Select JSON File", []string{".json"},
					globalConfig.DevVideoMapFile, func(filename string) {
						globalConfig.DevVideoMapFile = filename
						ui.jsonSelectDialog = nil
					})
				ui.jsonSelectDialog.Activate()
			}
			imgui.TableNextColumn()
			if globalConfig.DevVideoMapFile != "" && imgui.Button("Clear##vid") {
				globalConfig.DevVideoMapFile = ""
			}

			imgui.EndTable()
		}

		if ui.jsonSelectDialog != nil {
			ui.jsonSelectDialog.Draw()
		}
	}

	imgui.End()
}

func (sim *Sim) GetWindVector(p Point2LL, alt float32) Point2LL {
	// TODO: have a better gust model?
	windKts := sim.Scenario.Wind.Speed
	if sim.Scenario.Wind.Gust > 0 {
		windKts += rand.Int31n(sim.Scenario.Wind.Gust)
	}

	// wind.dir is where it's coming from, so +180 to get the vector that
	// affects the aircraft's course.
	d := float32(sim.Scenario.Wind.Direction + 180)
	vWind := [2]float32{sin(radians(d)), cos(radians(d))}
	vWind = scale2f(vWind, float32(windKts)/3600)
	return nm2ll(vWind)
}

///////////////////////////////////////////////////////////////////////////
// Spawning aircraft

func sampleRateMap(rates map[string]*int32) (string, int) {
	// Choose randomly in proportion to the rates in the map
	rateSum := 0
	var result string
	for item, rate := range rates {
		rateSum += int(*rate)
		// Weighted reservoir sampling...
		if rand.Float32() < float32(int(*rate))/float32(rateSum) {
			result = item
		}
	}
	return result, rateSum
}

func (sim *Sim) SpawnAircraft() {
	now := sim.CurrentTime()

	addAircraft := func(ac *Aircraft) {
		if _, ok := sim.Aircraft[ac.Callsign]; ok {
			lg.Errorf("%s: already have an aircraft with that callsign!", ac.Callsign)
			return
		}
		sim.Aircraft[ac.Callsign] = ac

		ac.RunWaypointCommands(ac.Waypoints[0].Commands)

		ac.Position = ac.Waypoints[0].Location
		if ac.Position.IsZero() {
			lg.Errorf("%s: uninitialized initial waypoint position!", ac.Callsign)
			return
		}
		ac.Heading = float32(ac.Waypoints[0].Heading)
		if ac.Heading == 0 { // unassigned, so get the heading from the next fix
			ac.Heading = headingp2ll(ac.Position, ac.Waypoints[1].Location, scenarioGroup.MagneticVariation)
		}
		ac.Waypoints = ac.Waypoints[1:]

		eventStream.Post(&AddedAircraftEvent{ac: ac})
	}

	randomWait := func(rate int) time.Duration {
		if rate == 0 {
			return 365 * 24 * time.Hour
		}
		avgSeconds := 3600 / float32(rate)
		seconds := lerp(rand.Float32(), .85*avgSeconds, 1.15*avgSeconds)
		return time.Duration(seconds * float32(time.Second))
	}

	for group, airportRates := range sim.ArrivalGroupRates {
		if now.After(sim.NextArrivalSpawn[group]) {
			arrivalAirport, rateSum := sampleRateMap(airportRates)

			if ac := sim.SpawnArrival(arrivalAirport, group); ac != nil {
				ac.FlightPlan.ArrivalAirport = arrivalAirport
				addAircraft(ac)
				sim.NextArrivalSpawn[group] = now.Add(randomWait(rateSum))
			}
		}
	}

	for airport, runwayTimes := range sim.NextDepartureSpawn {
		for runway, spawnTime := range runwayTimes {
			if !now.After(spawnTime) {
				continue
			}

			// Figure out which category to launch
			category, rateSum := sampleRateMap(sim.DepartureRates[airport][runway])
			if rateSum == 0 {
				lg.Errorf("%s/%s: couldn't find a matching runway for spawning departure?", airport, runway)
				continue
			}

			ap := scenarioGroup.Airports[airport]
			idx := FindIf(sim.Scenario.DepartureRunways,
				func(r ScenarioGroupDepartureRunway) bool {
					return r.Airport == airport && r.Runway == runway && r.Category == category
				})
			if idx == -1 {
				lg.Errorf("%s/%s/%s: couldn't find airport/runway/category for spawning departure. rates %s dep runways %s", airport, runway, category, spew.Sdump(sim.DepartureRates[airport][runway]), spew.Sdump(sim.Scenario.DepartureRunways))
				continue
			}

			if ac := sim.SpawnDeparture(ap, &sim.Scenario.DepartureRunways[idx]); ac != nil {
				ac.FlightPlan.DepartureAirport = airport
				addAircraft(ac)
				sim.NextDepartureSpawn[airport][runway] = now.Add(randomWait(rateSum))
			}
		}
	}
}

var badCallsigns map[string]interface{} = map[string]interface{}{
	// 9/11
	"AAL11":  nil,
	"UAL175": nil,
	"AAL77":  nil,
	"UAL93":  nil,

	// Pilot suicide
	"MAS17":   nil,
	"MAS370":  nil,
	"GWI18G":  nil,
	"GWI9525": nil,
	"MSR990":  nil,

	// Hijackings
	"FDX705":  nil,
	"AFR8969": nil,

	// Selected major crashes (leaning toward callsigns vice uses or is
	// likely to use in the future, via
	// https://en.wikipedia.org/wiki/List_of_deadliest_aircraft_accidents_and_incidents
	"PAA1736": nil,
	"KLM4805": nil,
	"JAL123":  nil,
	"AIC182":  nil,
	"AAL191":  nil,
	"PAA103":  nil,
	"KAL007":  nil,
	"AAL587":  nil,
	"CAL140":  nil,
	"TWA800":  nil,
	"SWR111":  nil,
	"KAL801":  nil,
	"AFR447":  nil,
	"CAL611":  nil,
	"LOT5055": nil,
	"ICE001":  nil,
}

func sampleAircraft(icao, fleet string) *Aircraft {
	al, ok := database.Airlines[icao]
	if !ok {
		// TODO: this should be caught at load validation time...
		lg.Errorf("Chose airline %s, not found in database", icao)
		return nil
	}

	if fleet == "" {
		fleet = "default"
	}

	fl, ok := al.Fleets[fleet]
	if !ok {
		// TODO: this also should be caught at validation time...
		lg.Errorf("Airline %s doesn't have a \"%s\" fleet!", icao, fleet)
		return nil
	}

	// Sample according to fleet count
	var aircraft string
	acCount := 0
	for _, ac := range fl {
		// Reservoir sampling...
		acCount += ac.Count
		if rand.Float32() < float32(ac.Count)/float32(acCount) {
			aircraft = ac.ICAO
		}
	}

	perf, ok := database.AircraftPerformance[aircraft]
	if !ok {
		// TODO: validation stage...
		lg.Errorf("Aircraft %s not found in performance database from fleet %+v, airline %s",
			aircraft, fleet, icao)
		return nil
	}

	// random callsign
	callsign := strings.ToUpper(icao)
	for {
		format := "####"
		if len(al.Callsign.CallsignFormats) > 0 {
			format = Sample(al.Callsign.CallsignFormats)
		}
		for {
			id := ""
			for _, ch := range format {
				switch ch {
				case '#':
					id += fmt.Sprintf("%d", rand.Intn(10))
				case '@':
					id += string(rune('A' + rand.Intn(26)))
				}
			}
			if id != "0" {
				callsign += id
				break
			}
		}
		// Only break and accept the callsign if it's not a bad one..
		if _, found := badCallsigns[callsign]; !found {
			break
		}
	}

	squawk := Squawk(rand.Intn(0o7000))

	acType := aircraft
	if perf.WeightClass == "H" {
		acType = "H/" + acType
	}
	if perf.WeightClass == "J" {
		acType = "J/" + acType
	}

	return &Aircraft{
		Callsign:       callsign,
		AssignedSquawk: squawk,
		Squawk:         squawk,
		Mode:           Charlie,
		FlightPlan: &FlightPlan{
			Rules:        IFR,
			AircraftType: acType,
		},

		Performance: perf,
	}
}

func (sim *Sim) SpawnArrival(airportName string, arrivalGroup string) *Aircraft {
	arrivals := scenarioGroup.ArrivalGroups[arrivalGroup]
	// Randomly sample from the arrivals that have a route to this airport.
	idx := SampleFiltered(arrivals, func(ar Arrival) bool {
		_, ok := ar.Airlines[airportName]
		return ok
	})
	if idx == -1 {
		lg.Errorf("unable to find route in arrival group %s for airport %s?!",
			arrivalGroup, airportName)
		return nil
	}
	arr := arrivals[idx]

	airline := Sample(arr.Airlines[airportName])
	ac := sampleAircraft(airline.ICAO, airline.Fleet)
	if ac == nil {
		return nil
	}

	ac.FlightPlan.DepartureAirport = airline.Airport
	ac.FlightPlan.ArrivalAirport = airportName
	ac.TrackingController = arr.InitialController
	ac.FlightPlan.Altitude = arr.CruiseAltitude
	if ac.FlightPlan.Altitude == 0 { // unspecified
		// try to figure out direction of flight
		pDep, depOk := scenarioGroup.Locate(ac.FlightPlan.DepartureAirport)
		pArr, arrOk := scenarioGroup.Locate(ac.FlightPlan.ArrivalAirport)
		if depOk && arrOk {
			if nmdistance2ll(pDep, pArr) < 100 {
				ac.FlightPlan.Altitude = 7000
			} else if nmdistance2ll(pDep, pArr) < 200 {
				ac.FlightPlan.Altitude = 11000
			} else if nmdistance2ll(pDep, pArr) < 300 {
				ac.FlightPlan.Altitude = 21000
			} else {
				ac.FlightPlan.Altitude = 37000
			}

			if headingp2ll(pDep, pArr, scenarioGroup.MagneticVariation) > 180 {
				ac.FlightPlan.Altitude += 1000
			}
		} else {
			ac.FlightPlan.Altitude = 39000
		}
	}
	ac.FlightPlan.Route = arr.Route
	// Start with the default waypoints for the arrival
	ac.Waypoints = arr.Waypoints
	// But if there is a custom route for any of the active runways, switch
	// to that. Results are undefined if there are multiple matches.
	for _, aprwy := range sim.Scenario.ArrivalRunways {
		if wp, ok := arr.RunwayWaypoints[aprwy.Runway]; ok {
			ac.Waypoints = wp
			break
		}
	}
	ac.Altitude = float32(arr.InitialAltitude)
	ac.IAS = float32(arr.InitialSpeed)
	ac.CrossingAltitude = arr.ClearedAltitude
	ac.CrossingSpeed = arr.SpeedRestriction
	ac.Scratchpad = arr.Scratchpad
	if arr.ExpectApproach != "" {
		if appr, ok := scenarioGroup.Airports[ac.FlightPlan.ArrivalAirport].Approaches[arr.ExpectApproach]; ok {
			ac.Approach = &appr
		} else {
			lg.Errorf("%s: unable to find expected %s approach", ac.Callsign, arr.ExpectApproach)
		}
	}

	if rand.Float32() < sim.GoAroundRate {
		sim.WillGoAround[ac.Callsign] = nil
	}

	return ac
}

func (sim *Sim) SpawnDeparture(ap *Airport, rwy *ScenarioGroupDepartureRunway) *Aircraft {
	var dep *Departure
	if rand.Float32() < sim.DepartureChallenge {
		// 50/50 split between the exact same departure and a departure to
		// the same gate as the last departure.
		if rand.Float32() < .5 {
			dep = rwy.lastDeparture
		} else if rwy.lastDeparture != nil {
			idx := SampleFiltered(ap.Departures,
				func(d Departure) bool {
					return ap.ExitCategories[d.Exit] == ap.ExitCategories[rwy.lastDeparture.Exit]
				})
			if idx == -1 {
				// This shouldn't ever happen...
				lg.Errorf("%s: unable to find a valid departure: %s", rwy.Runway, spew.Sdump(ap))
				return nil
			}
			dep = &ap.Departures[idx]
		}
	}

	if dep == nil {
		// Sample uniformly, minding the category, if specified
		idx := SampleFiltered(ap.Departures,
			func(d Departure) bool {
				return rwy.Category == "" || rwy.Category == ap.ExitCategories[d.Exit]
			})
		if idx == -1 {
			// This shouldn't ever happen...
			lg.Errorf("%s: unable to find a valid departure: %s", rwy.Runway, spew.Sdump(ap))
			return nil
		}
		dep = &ap.Departures[idx]
	}

	rwy.lastDeparture = dep

	airline := Sample(dep.Airlines)
	ac := sampleAircraft(airline.ICAO, airline.Fleet)

	exitRoute := rwy.exitRoutes[dep.Exit]
	ac.Waypoints = DuplicateSlice(exitRoute.Waypoints)
	ac.Waypoints = append(ac.Waypoints, dep.routeWaypoints...)

	ac.FlightPlan.Route = exitRoute.InitialRoute + " " + dep.Route
	ac.FlightPlan.ArrivalAirport = dep.Destination
	ac.Scratchpad = scenarioGroup.Scratchpads[dep.Exit]
	if dep.Altitude == 0 {
		// If unspecified, pick something in the flight levels...
		// TODO: get altitudes right considering East/West-bound...
		ac.FlightPlan.Altitude = 28000 + 1000*rand.Intn(13)
	} else {
		ac.FlightPlan.Altitude = dep.Altitude
	}

	ac.TrackingController = ap.DepartureController
	ac.Altitude = float32(ap.Elevation)
	ac.AssignedAltitude = exitRoute.ClearedAltitude

	return ac
}
