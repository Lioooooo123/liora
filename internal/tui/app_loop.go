package tui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

func (a *App) runBlocking(ctx context.Context, input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	for {
		fmt.Fprint(output, "\n"+promptStyle.Render("liora")+" > ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		switch line {
		case "/exit", "/quit":
			fmt.Fprintln(output, "Bye")
			return nil
		case "/help":
			a.renderSection(output, "Help", helpText())
			continue
		}
		if strings.HasPrefix(line, "/") && a.config.Commands != nil {
			result, handled, err := a.config.Commands.HandleCommand(ctx, line)
			if err != nil {
				fmt.Fprintf(output, "Error: %v\n", err)
				continue
			}
			if handled {
				a.renderSection(output, commandResultTitle(line), result)
				continue
			}
			a.renderSection(output, "System", "Unknown command. Use /help to view available commands.")
			continue
		}
		if strings.HasPrefix(line, "/") {
			a.renderSection(output, "System", "Unknown command. Use /help to view available commands.")
			continue
		}
		if err := a.runTurn(ctx, line, output); err != nil {
			fmt.Fprintf(output, "Error: %v\n", err)
		}
	}
	return scanner.Err()
}

type inputLine struct {
	line string
	ok   bool
	err  error
}

type turnOutcome struct {
	err error
}

type streamingLoop struct {
	app          *App
	ctx          context.Context
	output       io.Writer
	streamer     StreamingSubmitter
	running      bool
	turnDone     <-chan turnOutcome
	streamEvents <-chan StreamUpdate
	renderer     *lineStreamRenderer
}

func (a *App) runStreaming(ctx context.Context, input io.Reader, output io.Writer, streamer StreamingSubmitter) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	lines := scanInput(ctx, input)
	loop := &streamingLoop{
		app:      a,
		ctx:      ctx,
		output:   output,
		streamer: streamer,
	}
	prompt := func() {
		fmt.Fprint(output, "\n"+promptStyle.Render("liora")+" > ")
	}
	prompt()
	var pending []string
	var inputClosed bool
	var scanErr error
	for {
		if !loop.running && len(pending) > 0 {
			line := pending[0]
			pending = pending[1:]
			if loop.handleLine(line) {
				cancel()
				return nil
			}
			continue
		}
		if inputClosed && !loop.running {
			return scanErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case result := <-loop.turnDone:
			loop.running = false
			loop.turnDone = nil
			if loop.streamEvents != nil {
				for event := range loop.streamEvents {
					loop.renderStreamUpdate(event)
				}
			}
			loop.flushStream()
			loop.streamEvents = nil
			if result.err != nil {
				fmt.Fprintf(output, "Error: %v\n", result.err)
			}
			if len(pending) == 0 && !inputClosed {
				prompt()
			}
		case event, ok := <-loop.streamEvents:
			if ok {
				loop.renderStreamUpdate(event)
			}
		case scanned := <-lines:
			if !scanned.ok {
				inputClosed = true
				scanErr = scanned.err
				continue
			}
			line := strings.TrimSpace(scanned.line)
			if line == "" {
				if !loop.running {
					prompt()
				}
				continue
			}
			if loop.running && !isRunningCommand(line) {
				pending = append(pending, line)
				continue
			}
			if loop.handleLine(line) {
				cancel()
				return nil
			}
		}
	}
}

func scanInput(ctx context.Context, input io.Reader) <-chan inputLine {
	lines := make(chan inputLine)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(input)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			case lines <- inputLine{line: scanner.Text(), ok: true}:
			}
		}
		select {
		case <-ctx.Done():
		case lines <- inputLine{ok: false, err: scanner.Err()}:
		}
	}()
	return lines
}

func (l *streamingLoop) handleLine(line string) bool {
	switch line {
	case "/exit", "/quit":
		fmt.Fprintln(l.output, "Bye")
		return true
	case "/help":
		l.app.renderSection(l.output, "Help", helpText())
		return false
	}
	if strings.HasPrefix(line, "/") && l.app.config.Commands != nil {
		result, handled, err := l.app.config.Commands.HandleCommand(l.ctx, line)
		if err != nil {
			fmt.Fprintf(l.output, "Error: %v\n", err)
			return false
		}
		if handled {
			l.app.renderSection(l.output, commandResultTitle(line), result)
			return false
		}
		l.app.renderSection(l.output, "System", "Unknown command. Use /help to view available commands.")
		return false
	}
	if strings.HasPrefix(line, "/") {
		l.app.renderSection(l.output, "System", "Unknown command. Use /help to view available commands.")
		return false
	}
	if l.running {
		l.app.renderSection(l.output, "System", "Task is still running. Use /cancel, /approve, /deny, or wait for it to finish.")
		return false
	}
	l.app.renderSection(l.output, "You", line)
	l.startTurn(line)
	return false
}

func (l *streamingLoop) startTurn(input string) {
	done := make(chan turnOutcome, 1)
	updates := make(chan StreamUpdate, 32)
	l.running = true
	l.turnDone = done
	l.streamEvents = updates
	l.renderer = newLineStreamRenderer(l.output, l.app.renderWidth())
	go func() {
		_, err := l.streamer.SubmitStream(l.ctx, input, func(update StreamUpdate) {
			updates <- update
		})
		close(updates)
		done <- turnOutcome{err: err}
	}()
}

func isRunningCommand(line string) bool {
	fields := strings.Fields(strings.TrimSpace(line))
	return len(fields) > 0 && fields[0] == "/cancel"
}

func (a *App) runTurn(ctx context.Context, input string, output io.Writer) error {
	if streamer, ok := a.submitter.(StreamingSubmitter); ok {
		renderer := newLineStreamRenderer(output, a.renderWidth())
		_, err := streamer.SubmitStream(ctx, input, func(update StreamUpdate) {
			renderer.Render(update)
		})
		renderer.Flush()
		return err
	}
	result, err := a.submitter.Submit(ctx, input)
	RenderTurnWithWidth(output, TurnView{
		Input:      input,
		ShowUser:   true,
		TurnResult: result,
	}, a.renderWidth())
	return err
}
