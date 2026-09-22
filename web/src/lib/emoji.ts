// A small curated emoji set with shortcodes for the chat picker and the
// :shortcode: autocomplete. Not the whole Unicode table on purpose: a
// couple of hundred entries cover what people type in a watch party.

export type Emoji = { char: string; names: string[] };
export type EmojiGroup = { name: string; emoji: Emoji[] };

const e = (char: string, ...names: string[]): Emoji => ({ char, names });

export const GROUPS: EmojiGroup[] = [
  {
    name: "Smileys",
    emoji: [
      e("😀", "grinning"), e("😃", "smiley"), e("😄", "smile"), e("😁", "grin"), e("😆", "laughing", "satisfied"),
      e("😅", "sweat_smile"), e("🤣", "rofl"), e("😂", "joy"), e("🙂", "slightly_smiling"), e("😉", "wink"),
      e("😊", "blush"), e("😇", "innocent"), e("🥰", "smiling_face_with_hearts"), e("😍", "heart_eyes"), e("🤩", "star_struck"),
      e("😘", "kissing_heart"), e("😋", "yum"), e("😛", "stuck_out_tongue"), e("😜", "stuck_out_tongue_winking_eye"), e("🤪", "zany"),
      e("🤑", "money_mouth"), e("🤗", "hugs"), e("🤭", "hand_over_mouth"), e("🤫", "shushing"), e("🤔", "thinking"),
      e("🤐", "zipper_mouth"), e("😐", "neutral_face"), e("😑", "expressionless"), e("😶", "no_mouth"), e("😏", "smirk"),
      e("😒", "unamused"), e("🙄", "roll_eyes"), e("😬", "grimacing"), e("🤥", "lying"), e("😌", "relieved"),
      e("😔", "pensive"), e("😪", "sleepy"), e("🤤", "drooling"), e("😴", "sleeping"), e("😷", "mask"),
      e("🤒", "face_with_thermometer"), e("🤮", "vomiting"), e("🥵", "hot"), e("🥶", "cold"), e("🥴", "woozy"),
      e("😵", "dizzy_face"), e("🤯", "exploding_head"), e("🤠", "cowboy"), e("🥳", "partying"), e("😎", "sunglasses"),
      e("🤓", "nerd"), e("🧐", "monocle"), e("😕", "confused"), e("😟", "worried"), e("🙁", "slightly_frowning"),
      e("😮", "open_mouth"), e("😯", "hushed"), e("😲", "astonished"), e("😳", "flushed"), e("🥺", "pleading"),
      e("😦", "frowning"), e("😨", "fearful"), e("😰", "cold_sweat"), e("😥", "disappointed_relieved"), e("😢", "cry"),
      e("😭", "sob"), e("😱", "scream"), e("😖", "confounded"), e("😣", "persevere"), e("😞", "disappointed"),
      e("😓", "sweat"), e("😩", "weary"), e("😫", "tired_face"), e("🥱", "yawning"), e("😤", "triumph"),
      e("😡", "rage", "pout"), e("😠", "angry"), e("🤬", "cursing"), e("😈", "smiling_imp"), e("💀", "skull"),
      e("💩", "poop", "hankey"), e("🤡", "clown"), e("👻", "ghost"), e("👽", "alien"), e("🤖", "robot"),
      e("😺", "smiley_cat"), e("😹", "joy_cat"), e("😻", "heart_eyes_cat"),
    ],
  },
  {
    name: "Gestures",
    emoji: [
      e("👋", "wave"), e("🤚", "raised_back_of_hand"), e("✋", "hand", "raised_hand"), e("🖖", "vulcan_salute"), e("👌", "ok_hand"),
      e("🤌", "pinched_fingers"), e("✌️", "v", "victory"), e("🤞", "crossed_fingers"), e("🤟", "love_you_gesture"), e("🤘", "metal"),
      e("🤙", "call_me_hand"), e("👈", "point_left"), e("👉", "point_right"), e("👆", "point_up_2"), e("👇", "point_down"),
      e("☝️", "point_up"), e("👍", "thumbsup", "+1"), e("👎", "thumbsdown", "-1"), e("✊", "fist"), e("👊", "punch"),
      e("👏", "clap"), e("🙌", "raised_hands"), e("🤝", "handshake"), e("🙏", "pray"), e("💪", "muscle"),
      e("🫡", "salute"), e("🤦", "facepalm"), e("🤷", "shrug"), e("👀", "eyes"), e("🧠", "brain"),
    ],
  },
  {
    name: "Hearts",
    emoji: [
      e("❤️", "heart"), e("🧡", "orange_heart"), e("💛", "yellow_heart"), e("💚", "green_heart"), e("💙", "blue_heart"),
      e("💜", "purple_heart"), e("🖤", "black_heart"), e("🤍", "white_heart"), e("💔", "broken_heart"), e("❣️", "heavy_heart_exclamation"),
      e("💕", "two_hearts"), e("💞", "revolving_hearts"), e("💓", "heartbeat"), e("💗", "heartpulse"), e("💖", "sparkling_heart"),
      e("💘", "cupid"), e("💝", "gift_heart"), e("💯", "100"), e("💢", "anger"), e("💥", "boom", "collision"),
      e("💫", "dizzy"), e("💦", "sweat_drops"), e("💤", "zzz"),
    ],
  },
  {
    name: "Watch party",
    emoji: [
      e("🍿", "popcorn"), e("🎬", "clapper"), e("🎥", "movie_camera"), e("📺", "tv"), e("🎞️", "film_strip"),
      e("🎟️", "tickets"), e("🎮", "video_game"), e("🎵", "musical_note"), e("🎶", "notes"), e("🎤", "microphone"),
      e("🎧", "headphones"), e("🎸", "guitar"), e("🥁", "drum"), e("🔥", "fire"), e("⭐", "star"),
      e("🌟", "star2"), e("✨", "sparkles"), e("🎉", "tada"), e("🎊", "confetti_ball"), e("🏆", "trophy"),
      e("🥇", "1st_place_medal"), e("🍕", "pizza"), e("🍔", "hamburger"), e("🍟", "fries"), e("🌮", "taco"),
      e("🍺", "beer"), e("🍻", "beers"), e("🍷", "wine_glass"), e("🥂", "champagne"), e("☕", "coffee"),
      e("🍵", "tea"), e("🧋", "bubble_tea"), e("🍩", "doughnut"), e("🍪", "cookie"), e("🍫", "chocolate_bar"),
      e("🛋️", "couch"), e("🌙", "crescent_moon"), e("😴", "zzz_face"), e("⏰", "alarm_clock"), e("⏳", "hourglass"),
    ],
  },
  {
    name: "Symbols",
    emoji: [
      e("✅", "white_check_mark"), e("❌", "x"), e("❓", "question"), e("❗", "exclamation"), e("⚠️", "warning"),
      e("🚫", "no_entry_sign"), e("💡", "bulb"), e("🔔", "bell"), e("🔇", "mute"), e("🔊", "loud_sound"),
      e("▶️", "arrow_forward"), e("⏸️", "pause_button"), e("⏭️", "next_track"), e("⏪", "rewind"), e("⏩", "fast_forward"),
      e("🔁", "repeat"), e("🔀", "shuffle"), e("🆗", "ok"), e("🆕", "new"), e("🔞", "underage"),
      e("👑", "crown"), e("💎", "gem"), e("🎯", "dart"), e("🚀", "rocket"), e("🐐", "goat"),
      e("🦄", "unicorn"), e("🐸", "frog"), e("🐱", "cat"), e("🐶", "dog"), e("🙈", "see_no_evil"),
      e("🙉", "hear_no_evil"), e("🙊", "speak_no_evil"),
    ],
  },
];

const ALL: Emoji[] = GROUPS.flatMap((g) => g.emoji);
const BY_NAME = new Map<string, string>();
for (const em of ALL) for (const n of em.names) if (!BY_NAME.has(n)) BY_NAME.set(n, em.char);

// searchEmoji returns entries whose shortcode contains the query, best
// (prefix) matches first.
export function searchEmoji(query: string, limit = 24): Emoji[] {
  const q = query.trim().toLowerCase().replace(/^:/, "");
  if (!q) return [];
  const prefix: Emoji[] = [];
  const rest: Emoji[] = [];
  for (const em of ALL) {
    if (em.names.some((n) => n.startsWith(q))) prefix.push(em);
    else if (em.names.some((n) => n.includes(q))) rest.push(em);
  }
  return [...prefix, ...rest].slice(0, limit);
}

// shortcodeQuery returns the ":prefix" being typed at the caret (at least
// two letters, so a plain colon in a timecode never opens the menu).
export function shortcodeQuery(text: string, caret: number): { start: number; prefix: string } | null {
  const before = text.slice(0, caret);
  const m = before.match(/(?:^|\s):([a-z0-9_+-]{2,})$/i);
  if (!m) return null;
  return { start: caret - m[1]!.length - 1, prefix: m[1]! };
}

// expandShortcodes replaces every known :name: with its emoji; unknown
// codes and timecodes are left alone.
export function expandShortcodes(text: string): string {
  return text.replace(/(^|\s):([a-z0-9_+-]+):/gi, (all, lead: string, name: string) => {
    const em = BY_NAME.get(name.toLowerCase());
    return em ? lead + em : all;
  });
}

// Recently used emoji, most recent first, remembered per browser.
const RECENT_KEY = "couchcast.emoji.recent";
const RECENT_MAX = 16;

export function recentEmoji(): string[] {
  try {
    const v = JSON.parse(localStorage.getItem(RECENT_KEY) ?? "[]") as unknown;
    return Array.isArray(v) ? v.filter((x): x is string => typeof x === "string").slice(0, RECENT_MAX) : [];
  } catch {
    return [];
  }
}

export function rememberEmoji(char: string) {
  try {
    const next = [char, ...recentEmoji().filter((c) => c !== char)].slice(0, RECENT_MAX);
    localStorage.setItem(RECENT_KEY, JSON.stringify(next));
  } catch {
    // storage unavailable
  }
}
