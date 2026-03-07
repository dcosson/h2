package session

import (
	"fmt"
	"net"
	"os"
	"runtime/debug"

	"h2/internal/automation"
	"h2/internal/session/message"
)

// acceptLoop accepts connections on the Unix socket and routes requests.
func (d *Daemon) acceptLoop() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "panic recovered in acceptLoop: %v\n%s\n", r, debug.Stack())
		}
	}()
	for {
		conn, err := d.Listener.Accept()
		if err != nil {
			return // listener closed
		}
		go d.handleConn(conn)
	}
}

func (d *Daemon) handleConn(conn net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "panic recovered in handleConn: %v\n%s\n", r, debug.Stack())
			conn.Close()
		}
	}()
	req, err := message.ReadRequest(conn)
	if err != nil {
		conn.Close()
		return
	}

	switch req.Type {
	case "send":
		d.handleSend(conn, req)
	case "show":
		d.handleShow(conn, req)
	case "status":
		d.handleStatus(conn)
	case "attach":
		d.handleAttach(conn, req)
	case "hook_event":
		d.handleHookEvent(conn, req)
	case "stop":
		d.handleStop(conn)
	case "trigger_add":
		d.handleTriggerAdd(conn, req)
	case "trigger_list":
		d.handleTriggerList(conn)
	case "trigger_remove":
		d.handleTriggerRemove(conn, req)
	case "schedule_add":
		d.handleScheduleAdd(conn, req)
	case "schedule_list":
		d.handleScheduleList(conn)
	case "schedule_remove":
		d.handleScheduleRemove(conn, req)
	default:
		message.SendResponse(conn, &message.Response{
			Error: "unknown request type: " + req.Type,
		})
		conn.Close()
	}
}

func (d *Daemon) handleSend(conn net.Conn, req *message.Request) {
	defer conn.Close()

	s := d.Session

	if req.Raw {
		// Raw mode: send body directly to PTY without prefix.
		// Uses interrupt priority so it bypasses the blocked-agent check
		// (the main use case is responding to permission prompts).
		id := message.EnqueueRaw(s.Queue, req.Body)
		message.SendResponse(conn, &message.Response{
			OK:        true,
			MessageID: id,
		})
		return
	}

	priority, ok := message.ParsePriority(req.Priority)
	if !ok {
		message.SendResponse(conn, &message.Response{
			Error: "invalid priority: " + req.Priority,
		})
		return
	}

	from := req.From
	if from == "" {
		from = "unknown"
	}

	id, err := message.PrepareMessage(s.Queue, s.Name(), from, req.Body, priority)
	if err != nil {
		message.SendResponse(conn, &message.Response{
			Error: err.Error(),
		})
		return
	}

	message.SendResponse(conn, &message.Response{
		OK:        true,
		MessageID: id,
	})
}

func (d *Daemon) handleShow(conn net.Conn, req *message.Request) {
	defer conn.Close()

	s := d.Session
	msg := s.Queue.Lookup(req.MessageID)
	if msg == nil {
		message.SendResponse(conn, &message.Response{
			Error: "message not found: " + req.MessageID,
		})
		return
	}

	info := &message.MessageInfo{
		ID:        msg.ID,
		From:      msg.From,
		Priority:  msg.Priority.String(),
		Status:    string(msg.Status),
		FilePath:  msg.FilePath,
		CreatedAt: msg.CreatedAt.Format("2006-01-02 15:04:05"),
	}
	if msg.DeliveredAt != nil {
		info.DeliveredAt = msg.DeliveredAt.Format("2006-01-02 15:04:05")
	}

	message.SendResponse(conn, &message.Response{
		OK:      true,
		Message: info,
	})
}

func (d *Daemon) handleStatus(conn net.Conn) {
	defer conn.Close()
	message.SendResponse(conn, &message.Response{
		OK:    true,
		Agent: d.AgentInfo(),
	})
}

func (d *Daemon) handleStop(conn net.Conn) {
	defer conn.Close()
	message.SendResponse(conn, &message.Response{OK: true})

	// Trigger graceful shutdown: set Quit so lifecycleLoop exits.
	s := d.Session
	s.Quit = true
	s.VT.KillChild()
	// Signal quitCh so lifecycleLoop unblocks if the child already exited
	// and the loop is waiting in the relaunch/quit select.
	select {
	case s.quitCh <- struct{}{}:
	default:
	}
}

func (d *Daemon) handleHookEvent(conn net.Conn, req *message.Request) {
	defer conn.Close()

	if req.EventName == "" {
		message.SendResponse(conn, &message.Response{
			Error: "event_name is required",
		})
		return
	}

	d.Session.HandleHookEvent(req.EventName, req.Payload)
	message.SendResponse(conn, &message.Response{OK: true})
}

func (d *Daemon) handleTriggerAdd(conn net.Conn, req *message.Request) {
	defer conn.Close()

	if d.TriggerEngine == nil {
		message.SendResponse(conn, &message.Response{Error: "trigger engine not initialized"})
		return
	}
	if req.Trigger == nil {
		message.SendResponse(conn, &message.Response{Error: "trigger spec is required"})
		return
	}

	t := triggerFromSpec(req.Trigger)
	if !d.TriggerEngine.Add(t) {
		message.SendResponse(conn, &message.Response{
			Error: fmt.Sprintf("trigger ID %q already exists", t.ID),
		})
		return
	}

	message.SendResponse(conn, &message.Response{OK: true, TriggerID: t.ID})
}

func (d *Daemon) handleTriggerList(conn net.Conn) {
	defer conn.Close()

	if d.TriggerEngine == nil {
		message.SendResponse(conn, &message.Response{OK: true})
		return
	}

	triggers := d.TriggerEngine.List()
	specs := make([]*message.TriggerSpec, len(triggers))
	for i, t := range triggers {
		specs[i] = specFromTrigger(t)
	}
	message.SendResponse(conn, &message.Response{OK: true, Triggers: specs})
}

func (d *Daemon) handleTriggerRemove(conn net.Conn, req *message.Request) {
	defer conn.Close()

	if d.TriggerEngine == nil {
		message.SendResponse(conn, &message.Response{Error: "trigger engine not initialized"})
		return
	}
	if req.TriggerID == "" {
		message.SendResponse(conn, &message.Response{Error: "trigger_id is required"})
		return
	}

	if !d.TriggerEngine.Remove(req.TriggerID) {
		message.SendResponse(conn, &message.Response{
			Error: fmt.Sprintf("trigger %q not found", req.TriggerID),
		})
		return
	}
	message.SendResponse(conn, &message.Response{OK: true})
}

func (d *Daemon) handleScheduleAdd(conn net.Conn, req *message.Request) {
	defer conn.Close()

	if d.ScheduleEngine == nil {
		message.SendResponse(conn, &message.Response{Error: "schedule engine not initialized"})
		return
	}
	if req.Schedule == nil {
		message.SendResponse(conn, &message.Response{Error: "schedule spec is required"})
		return
	}

	s := scheduleFromSpec(req.Schedule)
	if err := d.ScheduleEngine.Add(s); err != nil {
		message.SendResponse(conn, &message.Response{Error: err.Error()})
		return
	}

	message.SendResponse(conn, &message.Response{OK: true, ScheduleID: s.ID})
}

func (d *Daemon) handleScheduleList(conn net.Conn) {
	defer conn.Close()

	if d.ScheduleEngine == nil {
		message.SendResponse(conn, &message.Response{OK: true})
		return
	}

	schedules := d.ScheduleEngine.List()
	specs := make([]*message.ScheduleSpec, len(schedules))
	for i, s := range schedules {
		specs[i] = specFromSchedule(s)
	}
	message.SendResponse(conn, &message.Response{OK: true, Schedules: specs})
}

func (d *Daemon) handleScheduleRemove(conn net.Conn, req *message.Request) {
	defer conn.Close()

	if d.ScheduleEngine == nil {
		message.SendResponse(conn, &message.Response{Error: "schedule engine not initialized"})
		return
	}
	if req.ScheduleID == "" {
		message.SendResponse(conn, &message.Response{Error: "schedule_id is required"})
		return
	}

	if !d.ScheduleEngine.Remove(req.ScheduleID) {
		message.SendResponse(conn, &message.Response{
			Error: fmt.Sprintf("schedule %q not found", req.ScheduleID),
		})
		return
	}
	message.SendResponse(conn, &message.Response{OK: true})
}

// Conversion helpers between wire specs and automation types.

func triggerFromSpec(s *message.TriggerSpec) *automation.Trigger {
	return &automation.Trigger{
		ID:        s.ID,
		Name:      s.Name,
		Event:     s.Event,
		State:     s.State,
		SubState:  s.SubState,
		Condition: s.Condition,
		Action: automation.Action{
			Exec:     s.Exec,
			Message:  s.Message,
			From:     s.From,
			Priority: s.Priority,
		},
	}
}

func specFromTrigger(t *automation.Trigger) *message.TriggerSpec {
	return &message.TriggerSpec{
		ID:        t.ID,
		Name:      t.Name,
		Event:     t.Event,
		State:     t.State,
		SubState:  t.SubState,
		Condition: t.Condition,
		Exec:      t.Action.Exec,
		Message:   t.Action.Message,
		From:      t.Action.From,
		Priority:  t.Action.Priority,
	}
}

func scheduleFromSpec(s *message.ScheduleSpec) *automation.Schedule {
	mode, _ := automation.ParseConditionMode(s.ConditionMode)
	return &automation.Schedule{
		ID:            s.ID,
		Name:          s.Name,
		Start:         s.Start,
		RRule:         s.RRule,
		Condition:     s.Condition,
		ConditionMode: mode,
		Action: automation.Action{
			Exec:     s.Exec,
			Message:  s.Message,
			From:     s.From,
			Priority: s.Priority,
		},
	}
}

func specFromSchedule(s *automation.Schedule) *message.ScheduleSpec {
	return &message.ScheduleSpec{
		ID:            s.ID,
		Name:          s.Name,
		Start:         s.Start,
		RRule:         s.RRule,
		Condition:     s.Condition,
		ConditionMode: s.ConditionMode.String(),
		Exec:          s.Action.Exec,
		Message:       s.Action.Message,
		From:          s.Action.From,
		Priority:      s.Action.Priority,
	}
}
