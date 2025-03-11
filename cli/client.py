#!/usr/bin/env python3
import curses
import json
import asyncio
import websockets
import aiortc
import uuid
import os
import sys
import argparse
from aiortc import RTCPeerConnection, RTCSessionDescription
from aiortc.contrib.signaling import object_from_string, object_to_string
from dataclasses import dataclass
from typing import Optional, Dict, List
from datetime import datetime

@dataclass
class FileTransfer:
    name: str
    size: int
    received: int = 0
    complete: bool = False

class P2PClient:
    def __init__(self, server_url: str):
        self.server_url = server_url
        self.websocket = None
        self.my_token = None
        self.peer_token = None
        self.messages: List[str] = []
        self.file_transfers: Dict[str, FileTransfer] = {}
        self.screen = None
        self.input_buffer = ""
        self.command_mode = False
        self.should_exit = False
        self.pc = None
        self.dc = None

    def init_screen(self):
        self.screen = curses.initscr()
        curses.start_color()
        curses.use_default_colors()
        curses.init_pair(1, curses.COLOR_GREEN, -1)  # Success
        curses.init_pair(2, curses.COLOR_YELLOW, -1) # Warning
        curses.init_pair(3, curses.COLOR_RED, -1)    # Error
        curses.init_pair(4, curses.COLOR_BLUE, -1)   # Info
        curses.noecho()
        curses.cbreak()
        self.screen.keypad(True)
        self.screen.timeout(100)  # Non-blocking input

    def cleanup_screen(self):
        if self.screen:
            self.screen.keypad(False)
            curses.echo()
            curses.nocbreak()
            curses.endwin()

    def draw_ui(self):
        self.screen.clear()
        height, width = self.screen.getmaxyx()

        # Draw title bar
        title = " P2P File Transfer "
        self.screen.addstr(0, 0, "=" * width, curses.color_pair(4))
        self.screen.addstr(0, (width - len(title)) // 2, title, curses.color_pair(4) | curses.A_BOLD)

        # Draw status line
        status = f" Token: {self.my_token or 'Not connected'} | Peer: {self.peer_token or 'None'} "
        self.screen.addstr(1, 0, status)

        # Draw data channel status if connected
        if self.dc:
            dc_status = f" Channel: {self.dc.readyState} "
            self.screen.addstr(2, 0, dc_status, curses.color_pair(1))

        # Draw active transfers
        transfer_y = 4
        if self.file_transfers:
            self.screen.addstr(transfer_y, 0, "File Transfers:", curses.color_pair(4))
            transfer_y += 1
            for ft in self.file_transfers.values():
                if ft.size > 0:
                    progress = min((ft.received / ft.size) * 100, 100)
                    bar_width = width - 30
                    filled = int((bar_width * progress) / 100)
                    bar = f"[{'#' * filled}{'-' * (bar_width - filled)}]"
                    status = f" {ft.name}: {progress:5.1f}% {bar}"
                else:
                    status = f" {ft.name}: Preparing..."
                self.screen.addstr(transfer_y, 0, status)
                transfer_y += 1

        # Draw message history
        msg_height = height - transfer_y - 3
        msg_y = transfer_y
        recent_messages = self.messages[-msg_height:]
        for msg in recent_messages:
            try:
                color = curses.color_pair(0)
                if msg.startswith("[ERROR]"):
                    color = curses.color_pair(3)
                elif msg.startswith("[INFO]"):
                    color = curses.color_pair(4)
                self.screen.addstr(msg_y, 0, msg[:width-1], color)
                msg_y += 1
            except curses.error:
                break

        # Draw input prompt
        prompt = ": " if self.command_mode else "> "
        help_text = "Commands: /connect <token>, /send <file>, /quit"
        try:
            self.screen.addstr(height-2, 0, help_text, curses.A_DIM)
            self.screen.addstr(height-1, 0, prompt + self.input_buffer)
        except curses.error:
            pass

        self.screen.refresh()

    async def setup_peer_connection(self, is_initiator=False):
        config = aiortc.RTCConfiguration(
            iceServers=[
                {"urls": ["stun:stun.l.google.com:19302"]}
            ]
        )
        self.pc = RTCPeerConnection(configuration=config)
        
        @self.pc.on("datachannel")
        def on_datachannel(channel):
            self.dc = channel
            self.setup_data_channel()

        if is_initiator:
            self.dc = self.pc.createDataChannel("file-transfer")
            self.setup_data_channel()

    def setup_data_channel(self):
        @self.dc.on("open")
        def on_open():
            self.add_message("[INFO] Data channel opened")

        @self.dc.on("message")
        def on_message(message):
            if isinstance(message, str):
                try:
                    data = json.loads(message)
                    if data["type"] == "file-info":
                        self.handle_file_info(data["info"])
                    elif data["type"] == "message":
                        self.add_message(f"Peer: {data['content']}")
                except json.JSONDecodeError:
                    self.add_message(f"Peer: {message}")
            else:
                # Binary data - file chunk
                self.handle_file_chunk(message)

    def handle_file_info(self, info):
        transfer_id = str(uuid.uuid4())
        self.file_transfers[transfer_id] = FileTransfer(
            name=info["name"],
            size=info["size"]
        )
        self.add_message(f"[INFO] Receiving file: {info['name']}")

    def handle_file_chunk(self, chunk):
        # In a real implementation, we'd track which file this belongs to
        for transfer in self.file_transfers.values():
            if not transfer.complete:
                transfer.received += len(chunk)
                if transfer.received >= transfer.size:
                    transfer.complete = True
                break

    def add_message(self, message: str):
        timestamp = datetime.now().strftime("%H:%M:%S")
        self.messages.append(f"[{timestamp}] {message}")

    async def handle_websocket_message(self, message):
        data = json.loads(message)
        msg_type = data.get('type')

        if msg_type == 'token':
            self.my_token = data['token']
            self.add_message(f"[INFO] Your token: {self.my_token}")
        
        elif msg_type == 'request':
            self.add_message(f"[INFO] Connection request from: {data['token']}")
            # Auto-accept for now
            await self.websocket.send(json.dumps({
                "type": "accept",
                "peerToken": data['token']
            }))
            await self.setup_peer_connection(False)
        
        elif msg_type == 'offer':
            self.add_message("[INFO] Received offer")
            desc = RTCSessionDescription(
                sdp=json.loads(data['sdp'])['sdp'],
                type=data['type']
            )
            await self.pc.setRemoteDescription(desc)
            answer = await self.pc.createAnswer()
            await self.pc.setLocalDescription(answer)
            
            await self.websocket.send(json.dumps({
                "type": "answer",
                "peerToken": self.peer_token,
                "sdp": json.dumps({
                    "sdp": self.pc.localDescription.sdp,
                    "type": self.pc.localDescription.type
                })
            }))
        
        elif msg_type in ('answer', 'ice'):
            # Would handle WebRTC signaling
            pass

    async def process_command(self, cmd: str):
        if not cmd.startswith('/'):
            # Regular message
            if self.dc and self.dc.readyState == "open":
                self.dc.send(json.dumps({
                    "type": "message",
                    "content": cmd
                }))
                self.add_message(f"Me: {cmd}")
            return

        parts = cmd[1:].split()
        if not parts:
            return

        command = parts[0]
        args = parts[1:]

        if command == 'connect':
            if len(args) != 1:
                self.add_message("[ERROR] Usage: /connect <token>")
                return
            self.peer_token = args[0]
            await self.setup_peer_connection(True)
            await self.websocket.send(json.dumps({
                "type": "connect",
                "peerToken": args[0]
            }))
            self.add_message(f"[INFO] Connecting to {args[0]}...")
        
        elif command == 'send':
            if not args or not self.dc:
                self.add_message("[ERROR] Usage: /send <filename> (must be connected)")
                return
            
            filename = " ".join(args)
            if not os.path.exists(filename):
                self.add_message(f"[ERROR] File not found: {filename}")
                return

            # Send file info
            file_size = os.path.getsize(filename)
            self.dc.send(json.dumps({
                "type": "file-info",
                "info": {
                    "name": os.path.basename(filename),
                    "size": file_size
                }
            }))

            # Read and send file in chunks
            chunk_size = 16384  # 16KB chunks
            with open(filename, 'rb') as f:
                while chunk := f.read(chunk_size):
                    await asyncio.sleep(0.001)  # Prevent blocking
                    self.dc.send(chunk)

            self.add_message(f"[INFO] File sent: {filename}")
        
        elif command == 'quit':
            self.should_exit = True

    async def input_loop(self):
        while not self.should_exit:
            try:
                c = self.screen.getch()
                if c == -1:  # No input
                    continue
                
                if c == ord('\n'):
                    if self.input_buffer:
                        await self.process_command(self.input_buffer)
                        self.input_buffer = ""
                elif c == curses.KEY_BACKSPACE or c == 127:
                    self.input_buffer = self.input_buffer[:-1]
                elif c == ord('/') and not self.input_buffer:
                    self.command_mode = True
                    self.input_buffer = "/"
                else:
                    self.input_buffer += chr(c)
                
                self.draw_ui()
                
            except Exception as e:
                self.add_message(f"[ERROR] Input error: {e}")

    async def websocket_loop(self):
        while not self.should_exit:
            try:
                self.websocket = await websockets.connect(self.server_url)
                self.add_message("[INFO] Connected to server")
                
                while not self.should_exit:
                    try:
                        message = await self.websocket.recv()
                        await self.handle_websocket_message(message)
                        self.draw_ui()
                    except websockets.exceptions.ConnectionClosed:
                        self.add_message("[ERROR] Server connection lost")
                        break
            except Exception as e:
                self.add_message(f"[ERROR] WebSocket error: {e}")
                await asyncio.sleep(5)

    async def main(self):
        try:
            self.init_screen()
            self.add_message("[INFO] Starting P2P File Transfer Client...")
            await asyncio.gather(
                self.websocket_loop(),
                self.input_loop()
            )
        finally:
            if self.pc:
                await self.pc.close()
            if self.websocket:
                await self.websocket.close()
            self.cleanup_screen()

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description='P2P File Transfer CLI Client')
    parser.add_argument('hostname', help='Server hostname (e.g., p2p.example.com)')
    parser.add_argument('-p', '--port', type=int, help='Server port (default: none)', default=None)
    args = parser.parse_args()

    # Construct WebSocket URL
    ws_url = f"wss://{args.hostname}"
    if args.port:
        ws_url += f":{args.port}"
    ws_url += "/ws"

    client = P2PClient(ws_url)
    try:
        asyncio.run(client.main())
    except KeyboardInterrupt:
        pass
