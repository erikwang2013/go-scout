package scout

// The project mascot: a scout probe that carries models into search engines.
// Its parts map onto the library:
//
//	lens    — the query layer (Builder): what gets searched
//	antenna — engine fan-out: one query, nine engine drivers
//	collar  — ModelObserver: saves and deletes sync themselves
//	cards   — the searchable documents being ferried to an index
const (
	// MascotName is the mascot's name, Chinese first (the project's primary
	// language) with the English reading beside it.
	MascotName = "小侦 (Scouty)"
	// MascotTagline is the one-line description of what the mascot is doing.
	MascotTagline = "One little scout, nine search engines."
)

// MascotASCII is a terminal portrait of the mascot, safe to print on any TTY
// (plain ASCII, no escape codes, no double-width characters) and to pipe into a
// file. The legend on the right names the part of the library each piece stands
// for; keep the art column pure ASCII or the labels drift out of line.
const MascotASCII = `go-scout mascot

      )  )  )
       \ | /                    lens    -> scout.Builder (the query layer)
    .---------.                 antenna -> one query, nine engine drivers
   /  o     o  \                collar  -> ModelObserver auto-sync
  |      .      |               cards   -> model documents entering an index
   \    ---    /
    '---------'
    \_________/
   [_] [_] [_] [_]`

// MascotSVG is the mascot drawing. It is byte-for-byte the same document as
// docs/mascot.svg (see TestMascotSVGMatchesDocs), so it can be served, embedded
// in an HTML report, or written next to generated assets.
const MascotSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 260 285" font-family="'PingFang SC','Microsoft YaHei',sans-serif">
  <title>go-scout 项目宠物：小侦 Scouty</title>
  <desc>戴放大镜风镜、系侦察兵领巾的搜索小侦，右手抱着待同步的文档卡片，天线持续发出检索信号。</desc>

  <!-- 地面投影 -->
  <ellipse cx="130" cy="264" rx="64" ry="9" fill="#e3e0d6"/>

  <!-- 检索信号：天线雷达弧 -->
  <g fill="none" stroke="#2f6f6a" stroke-width="3.5" stroke-linecap="round">
    <path d="M 136.8 31.1 A 22 22 0 0 1 150.9 45.2"/>
    <path d="M 140.5 19.7 A 34 34 0 0 1 162.3 41.5" opacity=".7"/>
    <path d="M 144.2 8.3 A 46 46 0 0 1 173.7 37.8" opacity=".45"/>
  </g>

  <!-- 天线 -->
  <line x1="130" y1="82" x2="130" y2="58" stroke="#4a4a4a" stroke-width="4" stroke-linecap="round"/>
  <circle cx="130" cy="52" r="8" fill="#2f6f6a" stroke="#4a4a4a" stroke-width="3"/>
  <circle cx="127.4" cy="49.4" r="2.4" fill="#ffffff" opacity=".9"/>

  <!-- 待同步的文档卡片（模型） -->
  <g transform="rotate(-8 205 217)">
    <rect x="182" y="188" width="46" height="58" rx="7" fill="#efece4" stroke="#4a4a4a" stroke-width="2.5"/>
  </g>
  <g transform="rotate(5 199 225)">
    <rect x="176" y="196" width="46" height="58" rx="7" fill="#fdfcf8" stroke="#4a4a4a" stroke-width="2.5"/>
    <rect x="185" y="205" width="9" height="9" rx="2.5" fill="#2f6f6a"/>
    <g stroke-linecap="round">
      <line x1="185" y1="224" x2="214" y2="224" stroke="#9a968c" stroke-width="3.5"/>
      <line x1="185" y1="234" x2="208" y2="234" stroke="#cfcbc0" stroke-width="3.5"/>
      <line x1="185" y1="244" x2="212" y2="244" stroke="#cfcbc0" stroke-width="3.5"/>
    </g>
  </g>

  <!-- 双脚 -->
  <g fill="#f4f3ee" stroke="#4a4a4a" stroke-width="2.5">
    <rect x="104" y="238" width="24" height="18" rx="9"/>
    <rect x="132" y="238" width="24" height="18" rx="9"/>
  </g>

  <!-- 身体 -->
  <rect x="86" y="170" width="88" height="76" rx="26" fill="#f4f3ee" stroke="#4a4a4a" stroke-width="2.5"/>
  <!-- 左臂 -->
  <rect x="64" y="194" width="20" height="42" rx="10" fill="#f4f3ee" stroke="#4a4a4a" stroke-width="2.5"/>
  <!-- 右臂：搭在卡片上 -->
  <rect x="162" y="196" width="20" height="42" rx="10" fill="#f4f3ee" stroke="#4a4a4a" stroke-width="2.5"/>

  <!-- 侦察兵领巾 -->
  <path d="M 87 172 Q 130 196 173 172 L 173 188 Q 130 212 87 188 Z" fill="#2f6f6a" stroke="#4a4a4a" stroke-width="2.5" stroke-linejoin="round"/>
  <path d="M 166 178 L 194 190 L 174 202 Z" fill="#2f6f6a" stroke="#4a4a4a" stroke-width="2.5" stroke-linejoin="round"/>

  <!-- 头：放大镜风镜 -->
  <circle cx="130" cy="130" r="50" fill="#dbe8e5" stroke="#4a4a4a" stroke-width="7"/>
  <path d="M 96.2 117.7 A 36 36 0 0 1 117.7 96.2" fill="none" stroke="#ffffff" stroke-width="6" stroke-linecap="round" opacity=".9"/>
  <g fill="#3a3a3a">
    <circle cx="114" cy="132" r="7"/>
    <circle cx="146" cy="132" r="7"/>
  </g>
  <g fill="#ffffff" opacity=".85">
    <circle cx="111.6" cy="129.4" r="2.3"/>
    <circle cx="143.6" cy="129.4" r="2.3"/>
  </g>
  <path d="M 118 152 Q 130 162 142 152" fill="none" stroke="#3a3a3a" stroke-width="3.5" stroke-linecap="round"/>
</svg>
`
