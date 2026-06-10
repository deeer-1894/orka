"""Unit tests for the UI-TARS parser + coordinate remapping."""

from __future__ import annotations

import base64
import io

from PIL import Image

from utils.uitars import parse_uitars, png_size, smart_resize

W, H = 1280, 720
# the resize space the model's coordinates live in for a 1280x720 screenshot
RH, RW = smart_resize(H, W)


def b64_png(w: int, h: int) -> str:
    buf = io.BytesIO()
    Image.new("RGB", (w, h)).save(buf, format="PNG")
    return base64.b64encode(buf.getvalue()).decode("ascii")


def test_png_size():
    assert png_size(b64_png(123, 45)) == (123, 45)


def test_smart_resize_multiples_and_bounds():
    rh, rw = smart_resize(720, 1280)
    assert rh % 28 == 0 and rw % 28 == 0
    # huge image must be scaled down under MAX_PIXELS
    rh, rw = smart_resize(8000, 8000)
    assert rh * rw <= 16384 * 28 * 28


def test_click_with_box_tokens_remaps():
    text = "Thought: 点击搜索框\nAction: click(start_box='<|box_start|>(644,364)<|box_end|>')"
    a = parse_uitars(text, W, H)
    assert a["action"] == "click"
    assert abs(a["x"] - 644 / RW * W) < 1e-6
    assert abs(a["y"] - 364 / RH * H) < 1e-6
    assert a["_thought"] == "点击搜索框"


def test_click_four_number_box_uses_center():
    a = parse_uitars("Action: click(start_box='(100,200,140,240)')", W, H)
    assert abs(a["x"] - 120 / RW * W) < 1e-6
    assert abs(a["y"] - 220 / RH * H) < 1e-6


def test_point_tag_format():
    a = parse_uitars("Action: click(point='<point>500 300</point>')", W, H)
    assert a["action"] == "click"
    assert abs(a["x"] - 500 / RW * W) < 1e-6


def test_double_and_right_click():
    assert parse_uitars("Action: left_double(start_box='(10,10)')", W, H)["count"] == 2
    assert parse_uitars("Action: right_single(start_box='(10,10)')", W, H)["button"] == "right"


def test_drag():
    a = parse_uitars("Action: drag(start_box='(10,20)', end_box='(30,40)')", W, H)
    assert a["action"] == "drag"
    assert a["x2"] > a["x"] and a["y2"] > a["y"]


def test_type_unescapes_and_keeps_newline():
    a = parse_uitars("Action: type(content='hello \\'world\\'\\n')", W, H)
    assert a == {"_thought": "", "action": "type", "text": "hello 'world'\n"}


def test_scroll_direction_with_point():
    a = parse_uitars("Action: scroll(start_box='(644,364)', direction='down')", W, H)
    assert a["action"] == "scroll" and a["direction"] == "down" and "x" in a


def test_navigate_and_back():
    a = parse_uitars("Action: navigate(content='https://example.com')", W, H)
    assert a == {"_thought": "", "action": "navigate", "url": "https://example.com"}
    assert parse_uitars("Action: navigate_back()", W, H)["action"] == "navigate_back"


def test_finished_and_call_user():
    a = parse_uitars("Thought: done\nAction: finished(content='The title is Example')", W, H)
    assert a["action"] == "done" and a["result"] == "The title is Example"
    a = parse_uitars("Thought: need login\nAction: call_user()", W, H)
    assert a["action"] == "call_user" and a["reason"] == "need login"


def test_hotkey_and_wait():
    assert parse_uitars("Action: hotkey(key='ctrl c')", W, H) == {
        "_thought": "",
        "action": "hotkey",
        "key": "ctrl c",
    }
    assert parse_uitars("Action: wait()", W, H)["action"] == "wait"


def test_garbage_and_unknown():
    assert parse_uitars("", W, H)["action"] == "error"
    assert parse_uitars("I will click the button", W, H)["action"] == "error"
    assert parse_uitars("Action: explode(now='1')", W, H)["action"] == "error"


def test_coords_clamped_into_screen():
    a = parse_uitars(f"Action: click(start_box='({RW + 500},{RH + 500})')", W, H)
    assert a["x"] <= W - 1 and a["y"] <= H - 1


def test_uitars_planner_message_window(monkeypatch):
    from agent.model import UITarsPlanner

    monkeypatch.setenv("UITARS_MAX_SHOTS", "3")
    p = UITarsPlanner()
    shots = [f"s{i}" for i in range(6)]
    responses = [f"r{i}" for i in range(5)]
    msgs = p._messages("find the title", shots, responses)
    # window of 3 shots -> prompt user msg + 2 (assistant, user) turn pairs
    assert len(msgs) == 5
    assert "find the title" in msgs[0]["content"][0]["text"]
    assert msgs[1] == {"role": "assistant", "content": "r3"}
    assert msgs[-1]["role"] == "user"
    assert msgs[-1]["content"][0]["image_url"]["url"].endswith("s5")
