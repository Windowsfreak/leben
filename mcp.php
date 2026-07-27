<?php
/**
 * Model Context Protocol (MCP) Server in PHP
 * Supports MCP SSE (Server-Sent Events) Transport + HTTP JSON-RPC 2.0
 */

require_once __DIR__ . '/config.php';
require_once __DIR__ . '/lib.php';

// Cross-Origin Resource Sharing (CORS) headers for remote MCP clients
header('Access-Control-Allow-Origin: *');
header('Access-Control-Allow-Methods: GET, POST, OPTIONS');
header('Access-Control-Allow-Headers: Content-Type, Authorization, X-Session-Id');

if ($_SERVER['REQUEST_METHOD'] === 'OPTIONS') {
    http_response_code(204);
    exit;
}

$action = $_GET['action'] ?? ($_SERVER['REQUEST_METHOD'] === 'GET' ? 'sse' : 'message');
$sessionId = $_GET['sessionId'] ?? ($_GET['session_id'] ?? ($_SERVER['HTTP_X_SESSION_ID'] ?? null));

// Helper: Session queue file path
function get_session_queue_file($sid) {
    $clean_sid = preg_replace('/[^a-zA-Z0-9_\-]/', '', $sid);
    return sys_get_temp_dir() . '/mcp_session_' . $clean_sid . '.json';
}

// -------------------------------------------------------------------------
// 1. SSE Stream Endpoint (GET /mcp.php?action=sse or GET /mcp.php)
// -------------------------------------------------------------------------
if ($action === 'sse') {
    // Disable all output buffering for real-time SSE streaming
    set_time_limit(0);
    ini_set('max_execution_time', '0');
    ini_set('output_buffering', 'off');
    ini_set('zlib.output_compression', 'off');

    while (ob_get_level()) {
        ob_end_clean();
    }
    ob_implicit_flush(true);

    header('Content-Type: text/event-stream');
    header('Cache-Control: no-cache');
    header('Connection: keep-alive');
    header('X-Accel-Buffering: no'); // Disable buffering in Nginx / Caddy

    // Generate unique session ID
    $sid = bin2hex(random_bytes(16));
    $queueFile = get_session_queue_file($sid);
    file_put_contents($queueFile, json_encode([]));

    // Base URI resolution
    $scheme = (!empty($_SERVER['HTTPS']) && $_SERVER['HTTPS'] !== 'off') ? 'https' : 'http';
    $host = $_SERVER['HTTP_HOST'] ?? 'localhost';
    $script = $_SERVER['SCRIPT_NAME'];
    $messageUrl = "{$scheme}://{$host}{$script}?action=message&sessionId={$sid}";

    // Emit initial 'endpoint' SSE event per MCP specification
    echo "event: endpoint\n";
    echo "data: {$messageUrl}\n\n";
    flush();

    $lastPing = time();

    // Event polling loop
    while (true) {
        if (connection_aborted()) {
            if (file_exists($queueFile)) {
                @unlink($queueFile);
            }
            break;
        }

        // Check for queued messages to stream
        if (file_exists($queueFile)) {
            $raw = file_get_contents($queueFile);
            $queue = json_decode($raw, true) ?: [];
            if (!empty($queue)) {
                file_put_contents($queueFile, json_encode([])); // clear queue
                foreach ($queue as $msg) {
                    echo "event: message\n";
                    echo "data: " . json_encode($msg, JSON_UNESCAPED_UNICODE) . "\n\n";
                    flush();
                }
            }
        }

        // Send periodic SSE keep-alive comment
        if (time() - $lastPing >= 15) {
            echo ": keepalive\n\n";
            flush();
            $lastPing = time();
        }

        usleep(200000); // 200ms sleep
    }
    exit;
}

// -------------------------------------------------------------------------
// 2. MCP Tools Definition Registry
// -------------------------------------------------------------------------
function get_mcp_tools() {
    $langs = array_keys(get_supported_languages());
    $target_langs = array_merge($langs, ['all']);

    return [
        [
            'name' => 'search_tiles',
            'description' => 'Semantic vector search across profile cards by query or returns ranking. Returns tile metadata, teasers, and similarity scores.',
            'inputSchema' => [
                'type' => 'object',
                'properties' => [
                    'q' => ['type' => 'string', 'description' => 'Natural language query or keywords (e.g., "IT consulting", "Krypto", "Parkour")'],
                    'lang' => ['type' => 'string', 'enum' => $langs, 'description' => 'Preferred language (default "de")'],
                    'limit' => ['type' => 'integer', 'description' => 'Maximum tiles to return (default 20)'],
                    'offset' => ['type' => 'integer', 'description' => 'Pagination offset (default 0)']
                ]
            ]
        ],
        [
            'name' => 'get_similar_tiles',
            'description' => 'Find tiles semantically similar to an existing card by name.',
            'inputSchema' => [
                'type' => 'object',
                'properties' => [
                    'name' => ['type' => 'string', 'description' => 'Name of the source tile (e.g. "hero", "finance", "it")'],
                    'lang' => ['type' => 'string', 'enum' => $langs, 'description' => 'Preferred language (default "de")'],
                    'limit' => ['type' => 'integer', 'description' => 'Maximum tiles to return (default 5)'],
                    'offset' => ['type' => 'integer', 'description' => 'Pagination offset (default 0)']
                ],
                'required' => ['name']
            ]
        ],
        [
            'name' => 'get_tile',
            'description' => 'Fetch full metadata and optional detailed article content for a specific card by name and language.',
            'inputSchema' => [
                'type' => 'object',
                'properties' => [
                    'name' => ['type' => 'string', 'description' => 'Name of the card (e.g. "hero", "finance", "it", "contact")'],
                    'lang' => ['type' => 'string', 'enum' => $langs, 'description' => 'Language code (default "de")'],
                    'include_content' => ['type' => 'boolean', 'description' => 'Whether to include the full HTML article content from file (default true)']
                ],
                'required' => ['name']
            ]
        ],
        [
            'name' => 'create_tile',
            'description' => 'Create a new card with title, summary, html_teaser, and auto-generated vector embedding via Ollama.',
            'inputSchema' => [
                'type' => 'object',
                'properties' => [
                    'name' => ['type' => 'string', 'description' => 'Unique slug identifier (e.g., "ai-consulting")'],
                    'title' => ['type' => 'string', 'description' => 'Display title of the tile'],
                    'language' => ['type' => 'string', 'enum' => $langs, 'description' => 'Language code (default "de")'],
                    'summary' => ['type' => 'string', 'description' => 'Structured summary for vector search indexing'],
                    'html_teaser' => ['type' => 'string', 'description' => 'Grid tile preview HTML snippet'],
                    'tags' => ['type' => 'array', 'items' => ['type' => 'string'], 'description' => 'Category tags'],
                    'type' => ['type' => 'string', 'enum' => ['doc', 'link'], 'description' => 'Card behavior type'],
                    'link' => ['type' => 'string', 'description' => 'Target URL if type is "link"'],
                    'content_file' => ['type' => 'string', 'description' => 'HTML content partial filename (e.g., "ai_de.html")'],
                    'accent_color' => ['type' => 'string', 'description' => 'Hex accent color (e.g. "#fbbf24")'],
                    'background' => ['type' => 'string', 'description' => 'Custom CSS background or image path'],
                    'visible' => ['type' => 'boolean', 'description' => 'Public visibility toggle'],
                    'sort_order' => ['type' => 'integer', 'description' => 'Grid display rank (default 100)']
                ],
                'required' => ['name', 'title', 'summary']
            ]
        ],
        [
            'name' => 'translate_tile',
            'description' => 'Translate an existing tile metadata and HTML content into a target language or all missing languages using AI translation.',
            'inputSchema' => [
                'type' => 'object',
                'properties' => [
                    'name' => ['type' => 'string', 'description' => 'Name of the tile to translate'],
                    'target_lang' => ['type' => 'string', 'enum' => $target_langs, 'description' => 'Target language code or "all" for all missing languages']
                ],
                'required' => ['name']
            ]
        ],
        [
            'name' => 'list_translation_status',
            'description' => 'Retrieve matrix of tile translation statuses across languages and identify stale translations.',
            'inputSchema' => [
                'type' => 'object',
                'properties' => [
                    'name' => ['type' => 'string', 'description' => 'Optional tile name filter']
                ]
            ]
        ]
    ];
}

// -------------------------------------------------------------------------
// Helper: Check matrix of tile translations across languages
// -------------------------------------------------------------------------
function check_translation_matrix($tile_name = null) {
    $db = get_db_connection();
    $sql = "SELECT name, language, title, content_file, updated_at FROM tiles";
    $params = [];
    if (!empty($tile_name)) {
        $sql .= " WHERE name = :name";
        $params[':name'] = $tile_name;
    }
    $sql .= " ORDER BY name ASC, language ASC";

    $stmt = $db->prepare($sql);
    $stmt->execute($params);
    $rows = $stmt->fetchAll(PDO::FETCH_ASSOC);

    $matrix = [];
    foreach ($rows as $row) {
        $name = $row['name'];
        $lang = $row['language'];
        if (!isset($matrix[$name])) {
            $matrix[$name] = [
                'name' => $name,
                'languages' => [],
                'missing_languages' => [],
                'stale' => false
            ];
        }
        $matrix[$name]['languages'][$lang] = [
            'title' => $row['title'],
            'content_file' => $row['content_file'],
            'updated_at' => $row['updated_at']
        ];
    }

    $all_supported_langs = ['de', 'en'];
    $result = [];
    foreach ($matrix as $name => $data) {
        $present_langs = array_keys($data['languages']);
        $missing = array_values(array_diff($all_supported_langs, $present_langs));
        $data['missing_languages'] = $missing;

        if (count($present_langs) > 1) {
            $times = array_map(function($l) { return strtotime($l['updated_at']); }, $data['languages']);
            if (abs(max($times) - min($times)) > 86400) {
                $data['stale'] = true;
            }
        }
        $result[] = $data;
    }

    return $result;
}

// -------------------------------------------------------------------------
// Helper: Check Admin Password Authentication against ADMIN_PASSWORD_HASH
// -------------------------------------------------------------------------
function is_mcp_authenticated($args = []) {
    if (!defined('ADMIN_PASSWORD_HASH')) {
        return false;
    }

    // 1. Password passed in tool arguments
    $provided = $args['password'] ?? ($args['auth_password'] ?? ($args['_auth']['password'] ?? null));
    if (!empty($provided)) {
        if (password_verify($provided, ADMIN_PASSWORD_HASH) || $provided === ADMIN_PASSWORD_HASH) {
            return true;
        }
    }

    // 2. Active PHP session
    if (session_status() === PHP_SESSION_NONE) {
        @session_start();
    }
    if (!empty($_SESSION['admin_logged_in'])) {
        return true;
    }

    // 3. HTTP Authorization header (Bearer or Basic)
    $authHeader = $_SERVER['HTTP_AUTHORIZATION'] ?? ($_SERVER['REDIRECT_HTTP_AUTHORIZATION'] ?? '');
    if ($authHeader) {
        if (strcasecmp(substr($authHeader, 0, 7), 'Bearer ') === 0) {
            $pass = trim(substr($authHeader, 7));
            if (password_verify($pass, ADMIN_PASSWORD_HASH) || $pass === ADMIN_PASSWORD_HASH) {
                return true;
            }
        } elseif (strcasecmp(substr($authHeader, 0, 6), 'Basic ') === 0) {
            $decoded = base64_decode(substr($authHeader, 6));
            if ($decoded && strpos($decoded, ':') !== false) {
                list(, $pass) = explode(':', $decoded, 2);
                if (password_verify($pass, ADMIN_PASSWORD_HASH) || $pass === ADMIN_PASSWORD_HASH) {
                    return true;
                }
            }
        }
    }

    // 4. Custom HTTP headers
    $xPass = $_SERVER['HTTP_X_ADMIN_PASSWORD'] ?? ($_SERVER['HTTP_X_API_KEY'] ?? '');
    if (!empty($xPass)) {
        if (password_verify($xPass, ADMIN_PASSWORD_HASH) || $xPass === ADMIN_PASSWORD_HASH) {
            return true;
        }
    }

    // 5. Query parameters
    $paramPass = $_GET['password'] ?? ($_GET['auth'] ?? null);
    if (!empty($paramPass)) {
        if (password_verify($paramPass, ADMIN_PASSWORD_HASH) || $paramPass === ADMIN_PASSWORD_HASH) {
            return true;
        }
    }

    return false;
}

// -------------------------------------------------------------------------
// 3. MCP Tool Dispatcher
// -------------------------------------------------------------------------
function execute_mcp_tool($tool_name, $args) {
    // Restrict administrative actions behind admin password auth
    $admin_tools = ['create_tile', 'translate_tile', 'list_translation_status'];
    if (in_array($tool_name, $admin_tools) && !is_mcp_authenticated($args)) {
        throw new Exception("Unauthorized: Admin password authentication required for administrative tool '{$tool_name}'.");
    }

    $db = get_db_connection();

    switch ($tool_name) {
        case 'search_tiles':
            $q = $args['q'] ?? null;
            $lang = $args['lang'] ?? 'de';
            $limit = isset($args['limit']) ? (int)$args['limit'] : 20;
            $offset = isset($args['offset']) ? (int)$args['offset'] : 0;
            return search_tiles($lang, $q, $offset, $limit);

        case 'get_similar_tiles':
            $name = $args['name'] ?? '';
            $lang = $args['lang'] ?? 'de';
            $limit = isset($args['limit']) ? (int)$args['limit'] : 5;
            $offset = isset($args['offset']) ? (int)$args['offset'] : 0;
            if (empty($name)) {
                throw new Exception("Tile name is required for similarity search.");
            }
            return get_similar_tiles($name, $lang, $limit, $offset);

        case 'get_tile':
            $name = $args['name'] ?? '';
            $lang = $args['lang'] ?? 'de';
            $include_content = $args['include_content'] ?? true;

            $stmt = $db->prepare("SELECT id, name, language, tags, title, html_teaser, summary, link, type, content_file, visible, accent_color, background, sort_order, created_at, updated_at FROM tiles WHERE name = :name AND language = :lang");
            $stmt->execute([':name' => $name, ':lang' => $lang]);
            $tile = $stmt->fetch();
            if (!$tile) {
                // Try fallback language
                $fallback = ($lang === 'de') ? 'en' : 'de';
                $stmt->execute([':name' => $name, ':lang' => $fallback]);
                $tile = $stmt->fetch();
            }
            if (!$tile) {
                throw new Exception("Tile '{$name}' not found.");
            }

            if ($include_content && !empty($tile['content_file'])) {
                $fpath = __DIR__ . '/content/' . $tile['content_file'];
                if (file_exists($fpath)) {
                    $tile['article_html'] = file_get_contents($fpath);
                }
            }
            return $tile;

        case 'create_tile':
            $name = trim($args['name'] ?? '');
            $title = trim($args['title'] ?? '');
            $language = strtolower(trim($args['language'] ?? 'de'));
            $summary = trim($args['summary'] ?? '');
            $html_teaser = trim($args['html_teaser'] ?? '');
            $link = trim($args['link'] ?? '');
            $type = trim($args['type'] ?? 'doc');
            $content_file = trim($args['content_file'] ?? '');
            $accent_color = trim($args['accent_color'] ?? '#fbbf24');
            $background = trim($args['background'] ?? '');
            $visible = isset($args['visible']) ? (bool)$args['visible'] : true;
            $sort_order = isset($args['sort_order']) ? (int)$args['sort_order'] : 100;
            $tags_arr = (array)($args['tags'] ?? []);
            $pg_tags = '{' . implode(',', array_map('trim', $tags_arr)) . '}';

            if (empty($name) || empty($title) || empty($summary)) {
                throw new Exception("Name, title, and summary are required.");
            }

            $check = $db->prepare("SELECT id FROM tiles WHERE name = :name AND language = :language");
            $check->execute([':name' => $name, ':language' => $language]);
            if ($check->fetch()) {
                throw new Exception("Card '{$name}' with language '{$language}' already exists.");
            }

            $doc_text = format_tile_document_text($name, $language, $tags_arr, $summary);
            $vector_str = null;
            try {
                $embedding = get_embedding($doc_text, 'document');
                $vector_str = array_to_postgres_vector($embedding);
            } catch (Exception $e) {
                $vector_str = array_to_postgres_vector(array_fill(0, 768, 0.0));
            }

            if (!empty($content_file) && !empty($html_teaser)) {
                if (preg_match('/^[a-zA-Z0-9_\-\.]+\.html$/', $content_file)) {
                    file_put_contents(__DIR__ . '/content/' . $content_file, $html_teaser);
                }
            }

            $sql = "
                INSERT INTO tiles (
                    name, language, tags, title, html_teaser,
                    summary, link, type, content_file,
                    visible, accent_color, background, embedding, sort_order, created_at, updated_at
                ) VALUES (
                    :name, :language, :tags, :title, :html_teaser,
                    :summary, :link, :type, :content_file,
                    :visible, :accent_color, :background, :embedding::vector, :sort_order,
                    CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
                ) RETURNING id
            ";
            $stmt = $db->prepare($sql);
            $stmt->execute([
                ':name' => $name,
                ':language' => $language,
                ':tags' => $pg_tags,
                ':title' => $title,
                ':html_teaser' => $html_teaser,
                ':summary' => $summary,
                ':link' => $link ?: null,
                ':type' => $type,
                ':content_file' => $content_file ?: null,
                ':visible' => $visible,
                ':accent_color' => $accent_color,
                ':background' => $background ?: null,
                ':embedding' => $vector_str,
                ':sort_order' => $sort_order
            ]);
            $new_id = $stmt->fetchColumn();

            return [
                'id' => (int)$new_id,
                'name' => $name,
                'language' => $language,
                'title' => $title,
                'summary' => $summary,
                'tags' => $tags_arr,
                'content_file' => $content_file ?: null
            ];

        case 'translate_tile':
            $name = trim($args['name'] ?? '');
            $target_lang = strtolower(trim($args['target_lang'] ?? 'all'));
            if (empty($name)) {
                throw new Exception("Name is required.");
            }

            return execute_auto_translate($name, $target_lang);

        case 'list_translation_status':
            $tile_name = $args['name'] ?? null;
            return check_translation_matrix($tile_name);

        default:
            throw new Exception("Unknown tool '{$tool_name}'.");
    }
}

// -------------------------------------------------------------------------
// 4. JSON-RPC Message Receiver (POST /mcp.php?action=message)
// -------------------------------------------------------------------------
header('Content-Type: application/json; charset=utf-8');

$rawInput = file_get_contents('php://input');
if (empty($rawInput)) {
    $rawInput = file_get_contents('php://stdin');
}
$request = json_decode($rawInput, true);

if (!is_array($request) || !isset($request['jsonrpc'])) {
    http_response_code(400);
    echo json_encode([
        'jsonrpc' => '2.0',
        'id' => null,
        'error' => ['code' => -32600, 'message' => 'Invalid JSON-RPC Request']
    ]);
    exit;
}

$id = $request['id'] ?? null;
$method = $request['method'] ?? '';
$params = $request['params'] ?? [];

$response = null;

try {
    switch ($method) {
        case 'initialize':
            $response = [
                'jsonrpc' => '2.0',
                'id' => $id,
                'result' => [
                    'protocolVersion' => '2024-11-05',
                    'capabilities' => [
                        'tools' => (object)[]
                    ],
                    'serverInfo' => [
                        'name' => 'leben-mcp-server',
                        'version' => '1.0.0'
                    ]
                ]
            ];
            break;

        case 'notifications/initialized':
            http_response_code(202);
            exit;

        case 'tools/list':
            $response = [
                'jsonrpc' => '2.0',
                'id' => $id,
                'result' => [
                    'tools' => get_mcp_tools()
                ]
            ];
            break;

        case 'tools/call':
            $tool_name = $params['name'] ?? '';
            $arguments = $params['arguments'] ?? [];
            
            $tool_result = execute_mcp_tool($tool_name, $arguments);
            
            $response = [
                'jsonrpc' => '2.0',
                'id' => $id,
                'result' => [
                    'content' => [
                        [
                            'type' => 'text',
                            'text' => json_encode($tool_result, JSON_PRETTY_PRINT | JSON_UNESCAPED_UNICODE)
                        ]
                    ]
                ]
            ];
            break;

        case 'ping':
            $response = [
                'jsonrpc' => '2.0',
                'id' => $id,
                'result' => (object)[]
            ];
            break;

        default:
            $response = [
                'jsonrpc' => '2.0',
                'id' => $id,
                'error' => [
                    'code' => -32601,
                    'message' => "Method '{$method}' not found."
                ]
            ];
            break;
    }
} catch (Exception $e) {
    $response = [
        'jsonrpc' => '2.0',
        'id' => $id,
        'result' => [
            'content' => [
                [
                    'type' => 'text',
                    'text' => json_encode(['error' => $e->getMessage()], JSON_UNESCAPED_UNICODE)
                ]
            ],
            'isError' => true
        ]
    ];
}

// Queue message for active SSE listener if sessionId present
if (!empty($sessionId)) {
    $queueFile = get_session_queue_file($sessionId);
    if (file_exists($queueFile)) {
        $raw = file_get_contents($queueFile);
        $queue = json_decode($raw, true) ?: [];
        $queue[] = $response;
        file_put_contents($queueFile, json_encode($queue));
    }
}

// Return direct JSON-RPC HTTP response
echo json_encode($response, JSON_UNESCAPED_UNICODE);
