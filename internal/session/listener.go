package session

import (
	"net"

	"h2/internal/session/message"
)

// acceptLoop accepts connections on the Unix socket and routes requests.
func (d *Daemon) acceptLoop() {
	for {
		conn, err := d.Listener.Accept()
		if err != nil {
			return // listener closed
		}
		go d.handleConn(conn)
	}
}

func (d *Daemon) handleConn(conn net.Conn) {
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
	default:
		message.SendResponse(conn, &message.Response{
			Error: "unknown request type: " + req.Type,
		})
		conn.Close()
	}
}

func (d *Daemon) handleSend(conn net.Conn, req *message.Request) {
	defer conn.Close()

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

	id, err := message.PrepareMessage(d.Session.Queue, d.Name, from, req.Body, priority)
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

	msg := d.Session.Queue.Lookup(req.MessageID)
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
